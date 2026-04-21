package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"time"

	"flint2-xray-web-console/internal/qr"
	"flint2-xray-web-console/internal/vless"
	"flint2-xray-web-console/internal/xray"
)

// registerClientRoutes wires the client CRUD + link/qr endpoints onto
// the provided mux. Called from Handler().
func (s *Server) registerClientRoutes(mux *nethttp.ServeMux) {
	mux.HandleFunc("POST /api/clients", s.handleCreateClient)
	mux.HandleFunc("PATCH /api/clients/{id}", s.handlePatchClient)
	mux.HandleFunc("DELETE /api/clients/{id}", s.handleDeleteClient)
	mux.HandleFunc("POST /api/clients/{id}/disable", s.handleDisableClient)
	mux.HandleFunc("POST /api/clients/{id}/enable", s.handleEnableClient)
	mux.HandleFunc("GET /api/clients/{id}/link", s.handleClientLink)
	mux.HandleFunc("GET /api/clients/{id}/qr.png", s.handleClientQR)
}

type createClientReq struct {
	Name string `json:"name"`
	Flow string `json:"flow"`
}

type patchClientReq struct {
	Name *string `json:"name,omitempty"`
	Flow *string `json:"flow,omitempty"`
}

func (s *Server) handleCreateClient(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req createClientReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}
	if req.Flow == "" {
		req.Flow = "xtls-rprx-vision"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	var created *xray.Client
	err := s.mutateConfig(ctx, func(f *xray.File) error {
		c, err := f.AddClient(req.Name, req.Flow)
		if err != nil {
			return err
		}
		created = c
		return nil
	})
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusCreated, clientBlock{
		ID:   created.ID,
		Name: created.Email,
		Flow: created.Flow,
	})
}

func (s *Server) handlePatchClient(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("id required"))
		return
	}
	var req patchClientReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	if req.Name == nil && req.Flow == nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("nothing to change"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	var patched *xray.Client
	err := s.mutateConfig(ctx, func(f *xray.File) error {
		c := f.FindClient(id)
		if c == nil {
			return notFound(id)
		}
		if req.Name != nil {
			trimmed := strings.TrimSpace(*req.Name)
			if trimmed == "" {
				return fmt.Errorf("name cannot be empty")
			}
			// Reject a rename that collides with another client.
			in, _ := f.PrimaryInbound()
			for _, other := range in.Settings.Clients {
				if other.ID != id && other.Email == trimmed {
					return fmt.Errorf("name %q is already taken", trimmed)
				}
			}
			c.Email = trimmed
		}
		if req.Flow != nil {
			c.Flow = *req.Flow
		}
		patched = c
		return nil
	})
	if err != nil {
		writeErr(w, statusForErr(err), err)
		return
	}
	writeJSON(w, nethttp.StatusOK, clientBlock{
		ID:   patched.ID,
		Name: patched.Email,
		Flow: patched.Flow,
	})
}

func (s *Server) handleDeleteClient(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// Permanent delete removes from live config AND from the disabled
	// store, so a formerly-disabled client doesn't linger there after
	// the operator meant to obliterate it.
	err := s.mutateConfig(ctx, func(f *xray.File) error {
		_, err := f.RemoveClient(id)
		if err != nil {
			// Allow deleting a client that's only in the disabled store —
			// we still handle that below.
			return nil
		}
		return nil
	})
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	// Best-effort removal from the disabled store; absence is fine.
	if _, err := s.Disabled.Remove(id); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("disabled store: %w", err))
		return
	}
	w.WriteHeader(nethttp.StatusNoContent)
}

func (s *Server) handleDisableClient(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	var removed *xray.Client
	err := s.mutateConfig(ctx, func(f *xray.File) error {
		c, err := f.RemoveClient(id)
		if err != nil {
			return notFound(id)
		}
		removed = c
		return nil
	})
	if err != nil {
		writeErr(w, statusForErr(err), err)
		return
	}
	if err := s.Disabled.Add(*removed); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("disabled store: %w", err))
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{"id": id, "status": "disabled"})
}

func (s *Server) handleEnableClient(w nethttp.ResponseWriter, r *nethttp.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// Pull out of the disabled store first; if the restart-with-client
	// step below fails we push it back to keep state consistent.
	removed, err := s.Disabled.Remove(id)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	if removed == nil {
		writeErr(w, nethttp.StatusNotFound, notFound(id))
		return
	}

	err = s.mutateConfig(ctx, func(f *xray.File) error {
		in, err := f.PrimaryInbound()
		if err != nil {
			return err
		}
		if in.Settings == nil {
			in.Settings = &xray.Settings{}
		}
		for _, existing := range in.Settings.Clients {
			if existing.ID == removed.ID {
				return fmt.Errorf("client %s is already enabled", removed.ID)
			}
		}
		in.Settings.Clients = append(in.Settings.Clients, *removed)
		return nil
	})
	if err != nil {
		// Roll back: put the client back in the disabled store.
		_ = s.Disabled.Add(*removed)
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, clientBlock{
		ID: removed.ID, Name: removed.Email, Flow: removed.Flow,
	})
}

func (s *Server) handleClientLink(w nethttp.ResponseWriter, r *nethttp.Request) {
	url, err := s.buildLink(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusForErr(err), err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{"url": url})
}

func (s *Server) handleClientQR(w nethttp.ResponseWriter, r *nethttp.Request) {
	url, err := s.buildLink(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusForErr(err), err)
		return
	}
	png, err := qr.PNG(url, 384, qr.Medium)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write(png)
}

// buildLink gathers everything the vless:// URL needs from the current
// xray config plus the cached derived public key. Returns a notFound
// error if the UUID isn't live (disabled clients don't get a link —
// they're offline by definition).
func (s *Server) buildLink(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("id required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	f, err := xray.Read(s.ConfPath)
	if err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}
	in, err := f.PrimaryInbound()
	if err != nil {
		return "", err
	}
	var client *xray.Client
	if in.Settings != nil {
		for i := range in.Settings.Clients {
			if in.Settings.Clients[i].ID == id {
				client = &in.Settings.Clients[i]
				break
			}
		}
	}
	if client == nil {
		return "", notFound(id)
	}
	if in.StreamSettings == nil || in.StreamSettings.RealitySettings == nil {
		return "", fmt.Errorf("no reality settings in config")
	}
	rs := in.StreamSettings.RealitySettings
	pub, err := s.publicKey(ctx, rs.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("derive public key: %w", err)
	}
	sni := ""
	if len(rs.ServerNames) > 0 {
		sni = rs.ServerNames[0]
	}
	shortID := ""
	if len(rs.ShortIDs) > 0 {
		shortID = rs.ShortIDs[0]
	}
	return vless.BuildURL(vless.Params{
		UUID:        client.ID,
		Host:        s.Cfg.ServerAddress,
		Port:        parsePort(in.Port),
		Flow:        client.Flow,
		Name:        client.Email,
		Network:     in.StreamSettings.Network,
		SNI:         sni,
		Fingerprint: rs.Fingerprint,
		PublicKey:   pub,
		ShortID:     shortID,
	})
}

// notFoundErr is a sentinel type so handlers can map it to HTTP 404
// without string-matching on error messages.
type notFoundErr string

func (e notFoundErr) Error() string { return fmt.Sprintf("client %s not found", string(e)) }

func notFound(id string) error { return notFoundErr(id) }

func statusForErr(err error) int {
	var nf notFoundErr
	if errors.As(err, &nf) {
		return nethttp.StatusNotFound
	}
	return nethttp.StatusInternalServerError
}
