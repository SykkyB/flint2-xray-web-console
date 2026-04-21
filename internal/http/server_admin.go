package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	nethttp "net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"flint2-xray-web-console/internal/xray"
)

// registerServerAdminRoutes adds the "server-admin" endpoints: Reality
// parameter edits, key regeneration, and enabling the stats API.
func (s *Server) registerServerAdminRoutes(mux *nethttp.ServeMux) {
	mux.HandleFunc("PATCH /api/server/reality", s.handlePatchReality)
	mux.HandleFunc("POST /api/server/regenerate-keys", s.handleRegenerateKeys)
	mux.HandleFunc("POST /api/server/enable-stats", s.handleEnableStats)
}

// realityPatchReq is the body for PATCH /api/server/reality. Every
// field is a pointer so the caller can send only what they want to
// change; nil means "leave alone".
type realityPatchReq struct {
	Dest        *string   `json:"dest,omitempty"`
	ServerNames *[]string `json:"serverNames,omitempty"`
	ShortIDs    *[]string `json:"shortIds,omitempty"`
	Fingerprint *string   `json:"fingerprint,omitempty"`
}

// shortIDRe bounds what we accept in shortIds: hex digits, even length
// (each byte is 2 hex chars), up to 16 bytes. xray itself would also
// reject malformed values via `xray -test`, but catching the shape
// here gives a clearer 400 than a restart failure.
var shortIDRe = regexp.MustCompile(`^[0-9a-fA-F]{0,32}$`)

func (s *Server) handlePatchReality(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req realityPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("parse body: %w", err))
		return
	}
	if req.Dest == nil && req.ServerNames == nil && req.ShortIDs == nil && req.Fingerprint == nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("nothing to change"))
		return
	}
	if req.ShortIDs != nil {
		for _, sid := range *req.ShortIDs {
			if !shortIDRe.MatchString(sid) {
				writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("shortId %q must be hex, even length, up to 32 chars", sid))
				return
			}
			if len(sid)%2 != 0 {
				writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("shortId %q must be even length", sid))
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	err := s.mutateConfig(ctx, func(f *xray.File) error {
		in, err := f.PrimaryInbound()
		if err != nil {
			return err
		}
		if in.StreamSettings == nil || in.StreamSettings.RealitySettings == nil {
			return fmt.Errorf("inbound has no realitySettings to patch")
		}
		rs := in.StreamSettings.RealitySettings
		if req.Dest != nil {
			rs.Dest = strings.TrimSpace(*req.Dest)
		}
		if req.ServerNames != nil {
			rs.ServerNames = append([]string(nil), *req.ServerNames...)
		}
		if req.ShortIDs != nil {
			rs.ShortIDs = append([]string(nil), *req.ShortIDs...)
		}
		if req.Fingerprint != nil {
			rs.Fingerprint = *req.Fingerprint
		}
		return nil
	})
	if err != nil {
		writeErr(w, statusForErr(err), err)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{"status": "ok"})
}

type regenerateKeysResp struct {
	PublicKey string   `json:"publicKey"`
	Warnings  []string `json:"warnings"`
}

func (s *Server) handleRegenerateKeys(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	kp, err := s.Keys.Generate(ctx)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}

	err = s.mutateConfig(ctx, func(f *xray.File) error {
		in, err := f.PrimaryInbound()
		if err != nil {
			return err
		}
		if in.StreamSettings == nil || in.StreamSettings.RealitySettings == nil {
			return fmt.Errorf("inbound has no realitySettings")
		}
		in.StreamSettings.RealitySettings.PrivateKey = kp.Private
		return nil
	})
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	// Cached public key was derived from the old private key; drop it.
	s.InvalidatePublicKey()

	writeJSON(w, nethttp.StatusOK, regenerateKeysResp{
		PublicKey: kp.Public,
		Warnings: []string{
			"all existing client links contain the old public key and will stop working; regenerate & redistribute them",
		},
	})
}

type enableStatsResp struct {
	APIAddress string `json:"apiAddress"`
	Status     string `json:"status"`
}

func (s *Server) handleEnableStats(w nethttp.ResponseWriter, r *nethttp.Request) {
	if s.Cfg.StatsAPI == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("stats_api is not set in panel config; cannot wire the api inbound"))
		return
	}
	host, portStr, err := net.SplitHostPort(s.Cfg.StatsAPI)
	if err != nil {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("stats_api %q: %w", s.Cfg.StatsAPI, err))
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("stats_api port %q is not a valid number", portStr))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	err = s.mutateConfig(ctx, func(f *xray.File) error {
		if f.API != nil || f.Stats != nil {
			return fmt.Errorf("stats api already enabled")
		}
		for _, in := range f.Inbounds {
			if in.Tag == "api" {
				return fmt.Errorf("an inbound with tag \"api\" already exists")
			}
		}
		f.API = json.RawMessage(`{"tag":"api","services":["StatsService"]}`)
		f.Stats = json.RawMessage(`{}`)
		f.Policy = json.RawMessage(`{"levels":{"0":{"statsUserUplink":true,"statsUserDownlink":true}},"system":{"statsInboundUplink":true,"statsInboundDownlink":true,"statsOutboundUplink":true,"statsOutboundDownlink":true}}`)

		// Append an "api -> api" routing rule, creating routing if it
		// didn't exist. We don't parse existing rules; if the operator
		// has custom routing, they can decide whether to keep it.
		apiRule := json.RawMessage(`{"type":"field","inboundTag":["api"],"outboundTag":"api"}`)
		f.Routing = mergeRoutingRule(f.Routing, apiRule)

		// Prepend the api inbound so it's easy to find when reading the
		// config by hand.
		portRaw, _ := json.Marshal(port)
		apiInbound := xray.Inbound{
			Listen:   host,
			Port:     portRaw,
			Protocol: "dokodemo-door",
			Tag:      "api",
			Settings: &xray.Settings{
				Extra: map[string]json.RawMessage{
					"address": json.RawMessage(`"127.0.0.1"`),
				},
			},
		}
		f.Inbounds = append([]xray.Inbound{apiInbound}, f.Inbounds...)
		return nil
	})
	if err != nil {
		writeErr(w, statusForErr(err), err)
		return
	}
	writeJSON(w, nethttp.StatusOK, enableStatsResp{
		APIAddress: s.Cfg.StatsAPI,
		Status:     "enabled",
	})
}

// mergeRoutingRule returns a routing block that includes rule. If the
// existing routing is nil or has no "rules" array, we emit a fresh
// object. Otherwise we append to the existing rules.
func mergeRoutingRule(existing, rule json.RawMessage) json.RawMessage {
	if len(existing) == 0 {
		out, _ := json.Marshal(map[string]any{
			"domainStrategy": "AsIs",
			"rules":          []json.RawMessage{rule},
		})
		return out
	}
	var r map[string]json.RawMessage
	if err := json.Unmarshal(existing, &r); err != nil || r == nil {
		// Malformed; replace.
		out, _ := json.Marshal(map[string]any{"rules": []json.RawMessage{rule}})
		return out
	}
	var rules []json.RawMessage
	if raw, ok := r["rules"]; ok {
		_ = json.Unmarshal(raw, &rules)
	}
	rules = append(rules, rule)
	rulesOut, _ := json.Marshal(rules)
	r["rules"] = rulesOut
	out, _ := json.Marshal(r)
	return out
}

