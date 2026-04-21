package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"flint2-xray-web-console/internal/config"
	"flint2-xray-web-console/internal/service"
	"flint2-xray-web-console/internal/xray"
)

// Minimal VLESS+Reality config matching the router's real shape.
const fixtureConfig = `{
  "log": { "loglevel": "warning" },
  "inbounds": [{
    "listen": "0.0.0.0",
    "port": 9443,
    "protocol": "vless",
    "settings": {
      "decryption": "none",
      "clients": [
        { "id": "11111111-1111-1111-1111-111111111111", "flow": "xtls-rprx-vision", "email": "alice" },
        { "id": "22222222-2222-2222-2222-222222222222", "flow": "xtls-rprx-vision", "email": "bob" }
      ]
    },
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "show": false,
        "dest": "www.cloudflare.com:443",
        "serverNames": ["www.cloudflare.com"],
        "privateKey": "FAKE_PRIVATE_KEY",
        "shortIds": ["deadbeef"],
        "fingerprint": "chrome"
      }
    }
  }],
  "outbounds": [{ "protocol": "freedom", "tag": "direct" }]
}`

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	confPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(confPath, []byte(fixtureConfig), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	cfg := &config.Config{
		Listen:        "127.0.0.1:8080",
		ServerAddress: "vpn.example.com",
		XrayConfig:    confPath,
		XrayBin:       "/usr/bin/xray",
		XrayInit:      "/etc/init.d/xray",
		DisabledStore: filepath.Join(dir, "disabled.json"),
		Auth: config.AuthConfig{
			Username:       "admin",
			PasswordBcrypt: string(hash),
		},
	}

	// Fake xray key derivation: returns a deterministic public key.
	keyRun := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("Public key: DERIVED_PUBKEY\n"), nil
	}
	// Fake service runner: reports "running".
	svcRun := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("running"), nil
	}

	srv := &Server{
		Cfg:      cfg,
		Service:  &service.Manager{InitScript: cfg.XrayInit, XrayBin: cfg.XrayBin, ConfigPath: confPath, Run: svcRun},
		Keys:     &xray.KeyTool{XrayBin: cfg.XrayBin, Run: keyRun},
		ConfPath: confPath,
	}
	return srv, confPath
}

func doAuthedRequest(h nethttp.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, nil)
	r.SetBasicAuth("admin", "pw")
	h.ServeHTTP(w, r)
	return w
}

func TestState_Shape(t *testing.T) {
	srv, _ := newTestServer(t)
	w := doAuthedRequest(srv.Handler(), "GET", "/api/state")
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}

	var got stateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\nbody=%s", err, w.Body.String())
	}

	if got.ServerAddress != "vpn.example.com" {
		t.Errorf("server_address: %q", got.ServerAddress)
	}
	if got.Server.Port != 9443 {
		t.Errorf("port: %d", got.Server.Port)
	}
	if got.Server.Flow != "xtls-rprx-vision" {
		t.Errorf("flow: %q", got.Server.Flow)
	}
	if got.Server.Reality.Dest != "www.cloudflare.com:443" {
		t.Errorf("dest: %q", got.Server.Reality.Dest)
	}
	if got.Server.Reality.PublicKey != "DERIVED_PUBKEY" {
		t.Errorf("pubkey: %q", got.Server.Reality.PublicKey)
	}
	if !got.Server.Reality.HasPrivate {
		t.Errorf("hasPrivateKey should be true")
	}
	if len(got.Clients) != 2 || got.Clients[0].Name != "alice" {
		t.Errorf("clients: %+v", got.Clients)
	}
	if got.StatsAPIEnabled {
		t.Errorf("stats should be disabled in fixture")
	}
	if !got.Service.Running {
		t.Errorf("service.running: %+v", got.Service)
	}

	// Critical: private key must not leak into the response.
	if strings.Contains(w.Body.String(), "FAKE_PRIVATE_KEY") {
		t.Fatalf("private key leaked in /api/state response")
	}
}

func TestState_RequiresAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/state", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != nethttp.StatusUnauthorized {
		t.Errorf("code: %d", w.Code)
	}
}

func TestPublicKeyCaching(t *testing.T) {
	srv, _ := newTestServer(t)
	calls := 0
	srv.Keys = &xray.KeyTool{
		XrayBin: "/usr/bin/xray",
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			calls++
			return []byte("Public key: CACHED\n"), nil
		},
	}

	for i := 0; i < 3; i++ {
		pub, err := srv.publicKey(context.Background(), "priv")
		if err != nil {
			t.Fatalf("publicKey: %v", err)
		}
		if pub != "CACHED" {
			t.Fatalf("pub: %q", pub)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 derivation call (then cached), got %d", calls)
	}

	srv.InvalidatePublicKey()
	if _, err := srv.publicKey(context.Background(), "priv"); err != nil {
		t.Fatalf("publicKey after invalidate: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 derivation calls after invalidate, got %d", calls)
	}
}
