package http

import (
	"bytes"
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
	"flint2-xray-web-console/internal/runner"
	"flint2-xray-web-console/internal/service"
	"flint2-xray-web-console/internal/store"
	"flint2-xray-web-console/internal/xray"
)

// clientTestEnv builds a Server wired to temp files with fake xray and
// init.d runners that never touch the real system. It also records how
// many times Restart was invoked so tests can assert the mutation flow.
type clientTestEnv struct {
	Srv        *Server
	ConfPath   string
	Disabled   *store.Disabled
	XrayTests  int
	InitCalls  int
	LastAction string
}

func newClientTestEnv(t *testing.T) *clientTestEnv {
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
		Auth:          config.AuthConfig{Username: "admin", PasswordBcrypt: string(hash)},
	}

	env := &clientTestEnv{
		ConfPath: confPath,
		Disabled: store.New(cfg.DisabledStore),
	}

	// Fake runner routes by binary: xray → test/x25519, initscript → actions.
	run := runner.CombinedFunc(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case cfg.XrayBin:
			if len(args) > 0 && args[0] == "-test" {
				env.XrayTests++
				return []byte("Configuration OK."), nil
			}
			if len(args) > 0 && args[0] == "x25519" {
				return []byte("Public key: DERIVED_PUBKEY\n"), nil
			}
		case cfg.XrayInit:
			env.InitCalls++
			if len(args) > 0 {
				env.LastAction = args[0]
			}
			return []byte(""), nil
		}
		return []byte(""), nil
	})

	env.Srv = &Server{
		Cfg:      cfg,
		Service:  &service.Manager{InitScript: cfg.XrayInit, XrayBin: cfg.XrayBin, ConfigPath: confPath, Run: run},
		Keys:     &xray.KeyTool{XrayBin: cfg.XrayBin, Run: run},
		Disabled: env.Disabled,
		ConfPath: confPath,
	}
	return env
}

func do(h nethttp.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Content-Type", "application/json")
	r.SetBasicAuth("admin", "pw")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCreateClient(t *testing.T) {
	env := newClientTestEnv(t)
	h := env.Srv.Handler()

	w := do(h, "POST", "/api/clients", map[string]string{"name": "carol"})
	if w.Code != nethttp.StatusCreated {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var created clientBlock
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Name != "carol" || len(created.ID) != 36 || created.Flow != "xtls-rprx-vision" {
		t.Errorf("created: %+v", created)
	}
	if env.XrayTests != 1 || env.InitCalls != 1 || env.LastAction != "restart" {
		t.Errorf("restart flow: tests=%d init=%d last=%q", env.XrayTests, env.InitCalls, env.LastAction)
	}

	// Disk has the new client.
	f, _ := xray.Read(env.ConfPath)
	in, _ := f.PrimaryInbound()
	if len(in.Settings.Clients) != 3 {
		t.Errorf("on-disk clients: got %d, want 3", len(in.Settings.Clients))
	}
}

func TestCreateClient_DuplicateName(t *testing.T) {
	env := newClientTestEnv(t)
	h := env.Srv.Handler()

	w := do(h, "POST", "/api/clients", map[string]string{"name": "alice"}) // already exists in fixture
	if w.Code != nethttp.StatusInternalServerError {
		t.Errorf("code=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "already exists") {
		t.Errorf("body should mention duplicate: %s", w.Body.String())
	}
	// No restart should have happened on failure.
	if env.InitCalls != 0 {
		t.Errorf("init.d should not be called on failed mutation, got %d", env.InitCalls)
	}
}

func TestPatchClient_Rename(t *testing.T) {
	env := newClientTestEnv(t)
	h := env.Srv.Handler()

	newName := "alice-renamed"
	w := do(h, "PATCH", "/api/clients/11111111-1111-1111-1111-111111111111",
		map[string]any{"name": newName})
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	f, _ := xray.Read(env.ConfPath)
	in, _ := f.PrimaryInbound()
	if in.Settings.Clients[0].Email != newName {
		t.Errorf("rename not persisted: %+v", in.Settings.Clients[0])
	}
}

func TestDisableEnable_RoundTrip(t *testing.T) {
	env := newClientTestEnv(t)
	h := env.Srv.Handler()

	// Disable alice.
	w := do(h, "POST", "/api/clients/11111111-1111-1111-1111-111111111111/disable", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("disable: code=%d body=%s", w.Code, w.Body.String())
	}
	// She should be out of the live config and into the disabled store.
	f, _ := xray.Read(env.ConfPath)
	in, _ := f.PrimaryInbound()
	for _, c := range in.Settings.Clients {
		if c.ID == "11111111-1111-1111-1111-111111111111" {
			t.Fatalf("alice still in live config after disable")
		}
	}
	disabled, _ := env.Disabled.List()
	if len(disabled) != 1 || disabled[0].Client.Email != "alice" {
		t.Errorf("disabled store: %+v", disabled)
	}

	// Re-enable: back in live config with the same UUID.
	w = do(h, "POST", "/api/clients/11111111-1111-1111-1111-111111111111/enable", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("enable: code=%d body=%s", w.Code, w.Body.String())
	}
	f, _ = xray.Read(env.ConfPath)
	in, _ = f.PrimaryInbound()
	found := false
	for _, c := range in.Settings.Clients {
		if c.ID == "11111111-1111-1111-1111-111111111111" {
			found = true
			if c.Email != "alice" {
				t.Errorf("name changed on re-enable: %q", c.Email)
			}
		}
	}
	if !found {
		t.Errorf("alice not back in live config after enable")
	}
	disabled, _ = env.Disabled.List()
	if len(disabled) != 0 {
		t.Errorf("disabled store should be empty, got %+v", disabled)
	}
}

func TestEnableClient_NotFound(t *testing.T) {
	env := newClientTestEnv(t)
	w := do(env.Srv.Handler(), "POST", "/api/clients/not-a-real-id/enable", nil)
	if w.Code != nethttp.StatusNotFound {
		t.Errorf("code: %d", w.Code)
	}
}

func TestDeleteClient(t *testing.T) {
	env := newClientTestEnv(t)
	h := env.Srv.Handler()

	w := do(h, "DELETE", "/api/clients/11111111-1111-1111-1111-111111111111", nil)
	if w.Code != nethttp.StatusNoContent {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	f, _ := xray.Read(env.ConfPath)
	in, _ := f.PrimaryInbound()
	for _, c := range in.Settings.Clients {
		if c.ID == "11111111-1111-1111-1111-111111111111" {
			t.Errorf("alice still present")
		}
	}
}

func TestClientLink(t *testing.T) {
	env := newClientTestEnv(t)
	h := env.Srv.Handler()

	w := do(h, "GET", "/api/clients/11111111-1111-1111-1111-111111111111/link", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	url := resp["url"]
	if !strings.HasPrefix(url, "vless://11111111") {
		t.Errorf("url: %q", url)
	}
	if !strings.Contains(url, "pbk=DERIVED_PUBKEY") {
		t.Errorf("url missing pbk: %q", url)
	}
	if !strings.HasSuffix(url, "#alice") {
		t.Errorf("url missing #alice: %q", url)
	}
}

func TestClientQR(t *testing.T) {
	env := newClientTestEnv(t)
	h := env.Srv.Handler()

	w := do(h, "GET", "/api/clients/11111111-1111-1111-1111-111111111111/qr.png", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type: %q", ct)
	}
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Errorf("not a PNG")
	}
}

func TestClientLink_NotFound(t *testing.T) {
	env := newClientTestEnv(t)
	w := do(env.Srv.Handler(), "GET", "/api/clients/does-not-exist/link", nil)
	if w.Code != nethttp.StatusNotFound {
		t.Errorf("code: %d body=%s", w.Code, w.Body.String())
	}
}
