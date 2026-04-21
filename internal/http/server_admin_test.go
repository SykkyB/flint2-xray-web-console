package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"strings"
	"testing"

	"flint2-xray-web-console/internal/xray"
)

func TestPatchReality(t *testing.T) {
	env := newClientTestEnv(t)
	h := env.Srv.Handler()

	body := map[string]any{
		"dest":        "www.microsoft.com:443",
		"serverNames": []string{"www.microsoft.com", "login.microsoftonline.com"},
		"shortIds":    []string{"deadbeef", "cafe"},
		"fingerprint": "firefox",
	}
	w := do(h, "PATCH", "/api/server/reality", body)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}

	f, _ := xray.Read(env.ConfPath)
	in, _ := f.PrimaryInbound()
	rs := in.StreamSettings.RealitySettings
	if rs.Dest != "www.microsoft.com:443" {
		t.Errorf("dest: %q", rs.Dest)
	}
	if len(rs.ServerNames) != 2 || rs.ServerNames[0] != "www.microsoft.com" {
		t.Errorf("serverNames: %v", rs.ServerNames)
	}
	if len(rs.ShortIDs) != 2 || rs.ShortIDs[1] != "cafe" {
		t.Errorf("shortIds: %v", rs.ShortIDs)
	}
	if rs.Fingerprint != "firefox" {
		t.Errorf("fingerprint: %q", rs.Fingerprint)
	}
	// Critical: the private key must be preserved across a PATCH that
	// didn't touch it.
	if rs.PrivateKey != "FAKE_PRIVATE_KEY" {
		t.Errorf("private key changed: %q", rs.PrivateKey)
	}
	if env.InitCalls != 1 || env.LastAction != "restart" {
		t.Errorf("restart: init=%d last=%q", env.InitCalls, env.LastAction)
	}
}

func TestPatchReality_BadShortID(t *testing.T) {
	env := newClientTestEnv(t)
	w := do(env.Srv.Handler(), "PATCH", "/api/server/reality",
		map[string]any{"shortIds": []string{"nothex"}})
	if w.Code != nethttp.StatusBadRequest {
		t.Errorf("code: %d body=%s", w.Code, w.Body.String())
	}
	// No restart on validation failure.
	if env.InitCalls != 0 {
		t.Errorf("init.d should not have been called, got %d", env.InitCalls)
	}

	// Odd-length should also fail even if all chars are hex.
	w = do(env.Srv.Handler(), "PATCH", "/api/server/reality",
		map[string]any{"shortIds": []string{"abc"}})
	if w.Code != nethttp.StatusBadRequest {
		t.Errorf("odd-length: code=%d", w.Code)
	}
}

func TestPatchReality_EmptyBody(t *testing.T) {
	env := newClientTestEnv(t)
	w := do(env.Srv.Handler(), "PATCH", "/api/server/reality", map[string]any{})
	if w.Code != nethttp.StatusBadRequest {
		t.Errorf("code: %d", w.Code)
	}
}

func TestRegenerateKeys(t *testing.T) {
	env := newClientTestEnv(t)

	// Override the runner so x25519 (no args) returns a fresh keypair
	// while other xray calls still answer correctly.
	env.Srv.Keys = &xray.KeyTool{
		XrayBin: env.Srv.Cfg.XrayBin,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if len(args) > 0 && args[0] == "x25519" && len(args) == 1 {
				return []byte("Private key: NEW_PRIV\nPublic key: NEW_PUB\n"), nil
			}
			if len(args) >= 2 && args[0] == "x25519" && args[1] == "-i" {
				return []byte("Public key: DERIVED_FROM_" + args[2] + "\n"), nil
			}
			return []byte("Configuration OK."), nil
		},
	}
	// Prime the public key cache so we can verify invalidation.
	if _, err := env.Srv.publicKey(context.Background(), "FAKE_PRIVATE_KEY"); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	w := do(env.Srv.Handler(), "POST", "/api/server/regenerate-keys", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp regenerateKeysResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.PublicKey != "NEW_PUB" {
		t.Errorf("public key in response: %q", resp.PublicKey)
	}
	if len(resp.Warnings) == 0 {
		t.Errorf("expected a warning about existing client links")
	}

	// On-disk config has the new private key.
	f, _ := xray.Read(env.ConfPath)
	in, _ := f.PrimaryInbound()
	if in.StreamSettings.RealitySettings.PrivateKey != "NEW_PRIV" {
		t.Errorf("private key on disk: %q", in.StreamSettings.RealitySettings.PrivateKey)
	}

	// Cache was invalidated: next derivation uses the new private key.
	pub, _ := env.Srv.publicKey(context.Background(), "NEW_PRIV")
	if pub != "DERIVED_FROM_NEW_PRIV" {
		t.Errorf("cache not invalidated, got %q", pub)
	}
}

func TestEnableStats(t *testing.T) {
	env := newClientTestEnv(t)
	env.Srv.Cfg.StatsAPI = "127.0.0.1:10085"

	w := do(env.Srv.Handler(), "POST", "/api/server/enable-stats", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}

	f, _ := xray.Read(env.ConfPath)
	if f.API == nil {
		t.Errorf("api block missing")
	}
	if f.Stats == nil {
		t.Errorf("stats block missing")
	}
	if f.Policy == nil {
		t.Errorf("policy block missing")
	}
	if f.Routing == nil {
		t.Errorf("routing block missing")
	}
	var routing map[string]any
	_ = json.Unmarshal(f.Routing, &routing)
	rules, _ := routing["rules"].([]any)
	if len(rules) < 1 {
		t.Errorf("routing rules empty: %v", routing)
	}

	// api inbound was prepended.
	if len(f.Inbounds) < 2 || f.Inbounds[0].Tag != "api" {
		t.Errorf("api inbound not first, got %+v", f.Inbounds)
	}
	if f.Inbounds[0].Listen != "127.0.0.1" {
		t.Errorf("api listen: %q", f.Inbounds[0].Listen)
	}

	// /api/state should now report stats as enabled.
	w2 := do(env.Srv.Handler(), "GET", "/api/state", nil)
	if !strings.Contains(w2.Body.String(), `"stats_api_enabled": true`) {
		t.Errorf("state did not report stats enabled: %s", w2.Body.String())
	}

	// Idempotency: second call must fail cleanly.
	w3 := do(env.Srv.Handler(), "POST", "/api/server/enable-stats", nil)
	if w3.Code == nethttp.StatusOK {
		t.Errorf("second enable-stats should fail, got 200: %s", w3.Body.String())
	}
}

func TestEnableStats_NoStatsAPIInConfig(t *testing.T) {
	env := newClientTestEnv(t)
	env.Srv.Cfg.StatsAPI = ""

	w := do(env.Srv.Handler(), "POST", "/api/server/enable-stats", nil)
	if w.Code != nethttp.StatusBadRequest {
		t.Errorf("code: %d body=%s", w.Code, w.Body.String())
	}
	if env.InitCalls != 0 {
		t.Errorf("restart should not happen, init=%d", env.InitCalls)
	}
}
