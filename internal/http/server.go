package http

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"sync"
	"time"

	"flint2-xray-web-console/internal/config"
	"flint2-xray-web-console/internal/service"
	"flint2-xray-web-console/internal/store"
	"flint2-xray-web-console/internal/xray"
)

// Server bundles the dependencies the HTTP handlers need. It is the one
// place that touches panel config, xray config, key derivation, and the
// service manager; handlers below are thin methods on it.
type Server struct {
	Cfg      *config.Config
	Service  *service.Manager
	Keys     *xray.KeyTool
	Disabled *store.Disabled
	ConfPath string // usually Cfg.XrayConfig, duplicated for convenience

	// PanelConfigPath is the path to panel.yaml itself. Used by
	// enable-stats to write stats_api back so Activity works without a
	// manual restart. Optional: tests leave it empty and the handler
	// skips persistence.
	PanelConfigPath string

	// writeMu serialises every mutation of the xray config. Holders must
	// also take care to call InvalidatePublicKey after any change that
	// could have touched realitySettings.privateKey.
	writeMu sync.Mutex

	// pubKey is the cached X25519 public key derived from the current
	// realitySettings.privateKey. It is populated lazily on first use and
	// invalidated whenever we rewrite the xray config.
	mu       sync.Mutex
	pubKey   string
	activity *activityTracker
}

// Handler returns a net/http handler with all routes registered and
// basic-auth applied.
func (s *Server) Handler() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/service/status", s.handleServiceStatus)
	s.registerClientRoutes(mux)
	s.registerServerAdminRoutes(mux)
	s.registerServiceRoutes(mux)
	s.registerLogRoutes(mux)
	s.registerActivityRoute(mux)
	s.registerUIRoutes(mux)
	return BasicAuth(s.Cfg.Auth.Username, s.Cfg.Auth.PasswordBcrypt, mux)
}

// InvalidatePublicKey drops the cached public key. Call after any write
// that might have changed the Reality private key.
func (s *Server) InvalidatePublicKey() {
	s.mu.Lock()
	s.pubKey = ""
	s.mu.Unlock()
}

// publicKey returns the cached public key, deriving it if needed.
func (s *Server) publicKey(ctx context.Context, priv string) (string, error) {
	s.mu.Lock()
	cached := s.pubKey
	s.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	pub, err := s.Keys.DerivePublic(ctx, priv)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.pubKey = pub
	s.mu.Unlock()
	return pub, nil
}

// stateResponse is the JSON shape served by GET /api/state. We keep it
// explicit (not the raw xray.File) so we control what's exposed — in
// particular, the Reality private key never leaves the server.
type stateResponse struct {
	ServerAddress   string                `json:"server_address"`
	Service         service.State         `json:"service"`
	Server          serverBlock           `json:"server"`
	Clients         []clientBlock         `json:"clients"`
	Disabled        []disabledClientBlock `json:"disabled"`
	StatsAPIEnabled bool                  `json:"stats_api_enabled"`
	Warnings        []string              `json:"warnings,omitempty"`
}

type disabledClientBlock struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Flow       string    `json:"flow,omitempty"`
	DisabledAt time.Time `json:"disabledAt"`
}

type serverBlock struct {
	Listen   string       `json:"listen"`
	Port     int          `json:"port"`
	Flow     string       `json:"flow,omitempty"`
	Reality  realityBlock `json:"reality"`
}

type realityBlock struct {
	Dest         string   `json:"dest"`
	ServerNames  []string `json:"serverNames"`
	ShortIDs     []string `json:"shortIds"`
	Fingerprint  string   `json:"fingerprint"`
	PublicKey    string   `json:"publicKey,omitempty"`
	HasPrivate   bool     `json:"hasPrivateKey"`
}

type clientBlock struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Flow string `json:"flow,omitempty"`
}

func (s *Server) handleState(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	f, err := xray.Read(s.ConfPath)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("read xray config: %w", err))
		return
	}
	in, err := f.PrimaryInbound()
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}

	resp := stateResponse{
		ServerAddress: s.Cfg.ServerAddress,
		Server: serverBlock{
			Listen: in.Listen,
			Port:   parsePort(in.Port),
			Flow:   primaryFlow(in),
		},
		StatsAPIEnabled: f.API != nil && f.Stats != nil,
	}

	if in.StreamSettings != nil && in.StreamSettings.RealitySettings != nil {
		rs := in.StreamSettings.RealitySettings
		rb := realityBlock{
			Dest:        rs.Dest,
			ServerNames: rs.ServerNames,
			ShortIDs:    rs.ShortIDs,
			Fingerprint: rs.Fingerprint,
			HasPrivate:  rs.PrivateKey != "",
		}
		if rs.PrivateKey != "" {
			pub, err := s.publicKey(ctx, rs.PrivateKey)
			if err != nil {
				resp.Warnings = append(resp.Warnings, fmt.Sprintf("derive public key: %v", err))
			} else {
				rb.PublicKey = pub
			}
		}
		resp.Server.Reality = rb
	}

	if in.Settings != nil {
		for _, c := range in.Settings.Clients {
			resp.Clients = append(resp.Clients, clientBlock{
				ID:   c.ID,
				Name: c.Email,
				Flow: c.Flow,
			})
		}
	}

	if s.Disabled != nil {
		disabled, err := s.Disabled.List()
		if err != nil {
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("disabled store: %v", err))
		} else {
			for _, d := range disabled {
				resp.Disabled = append(resp.Disabled, disabledClientBlock{
					ID:         d.Client.ID,
					Name:       d.Client.Email,
					Flow:       d.Client.Flow,
					DisabledAt: d.DisabledAt,
				})
			}
		}
	}

	st, err := s.Service.Status(ctx)
	if err != nil {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("service status: %v", err))
	} else {
		resp.Service = st
	}

	writeJSON(w, nethttp.StatusOK, resp)
}

func (s *Server) handleServiceStatus(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	st, err := s.Service.Status(ctx)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, st)
}

// parsePort extracts an integer from xray's inbound port field, which is
// a json.RawMessage because xray also accepts strings like "1000-2000".
// For a range or anything non-numeric we return 0 and let the caller
// decide how to surface it.
func parsePort(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	return 0
}

// primaryFlow returns the flow value from the first client, on the
// assumption (true for our config) that every client in the inbound uses
// the same flow. If they diverge we fall back to empty.
func primaryFlow(in *xray.Inbound) string {
	if in.Settings == nil || len(in.Settings.Clients) == 0 {
		return ""
	}
	first := in.Settings.Clients[0].Flow
	for _, c := range in.Settings.Clients[1:] {
		if c.Flow != first {
			return ""
		}
	}
	return first
}

func writeJSON(w nethttp.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w nethttp.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
