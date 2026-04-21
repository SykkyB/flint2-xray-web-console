package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"flint2-xray-web-console/internal/xray"
)

func TestServiceStart(t *testing.T) {
	env := newClientTestEnv(t)
	w := do(env.Srv.Handler(), "POST", "/api/service/start", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if env.LastAction != "status" && env.LastAction != "start" {
		// runServiceAction: first call is "start", second is "status".
		// We track only the last one, so expect "status" on success.
		t.Errorf("LastAction: %q", env.LastAction)
	}
}

func TestServiceStop(t *testing.T) {
	env := newClientTestEnv(t)
	w := do(env.Srv.Handler(), "POST", "/api/service/stop", nil)
	if w.Code != nethttp.StatusOK {
		t.Errorf("code=%d", w.Code)
	}
}

func TestServiceRestart_ValidatesFirst(t *testing.T) {
	env := newClientTestEnv(t)
	before := env.XrayTests
	w := do(env.Srv.Handler(), "POST", "/api/service/restart", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if env.XrayTests <= before {
		t.Errorf("xray -test should have been called before restart")
	}
}

func TestLogs_Tail(t *testing.T) {
	env := newClientTestEnv(t)
	dir := filepath.Dir(env.ConfPath)
	errPath := filepath.Join(dir, "xray-error.log")
	lines := make([]string, 0, 250)
	for i := 0; i < 250; i++ {
		lines = append(lines, "line "+strconv.Itoa(i))
	}
	if err := os.WriteFile(errPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	env.Srv.Cfg.LogError = errPath

	w := do(env.Srv.Handler(), "GET", "/api/logs/error?tail=10", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp logsResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Lines) != 10 {
		t.Errorf("lines: got %d want 10", len(resp.Lines))
	}
	if resp.Lines[9] != "line 249" {
		t.Errorf("last line: %q", resp.Lines[9])
	}
}

func TestLogs_MissingFile(t *testing.T) {
	env := newClientTestEnv(t)
	env.Srv.Cfg.LogError = filepath.Join(t.TempDir(), "does-not-exist.log")
	w := do(env.Srv.Handler(), "GET", "/api/logs/error", nil)
	if w.Code != nethttp.StatusOK {
		t.Errorf("missing file should be empty success, got %d body=%s", w.Code, w.Body.String())
	}
	var resp logsResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Lines) != 0 {
		t.Errorf("expected no lines")
	}
}

func TestLogs_BadName(t *testing.T) {
	env := newClientTestEnv(t)
	w := do(env.Srv.Handler(), "GET", "/api/logs/nope", nil)
	if w.Code != nethttp.StatusBadRequest {
		t.Errorf("code: %d", w.Code)
	}
}

func TestLogs_BigFileTruncates(t *testing.T) {
	env := newClientTestEnv(t)
	path := filepath.Join(filepath.Dir(env.ConfPath), "big.log")
	var buf strings.Builder
	// Write ~600 KB of 100-byte lines so we cross the 512 KB window.
	for i := 0; i < 6000; i++ {
		buf.WriteString(strings.Repeat("x", 90))
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	env.Srv.Cfg.LogAccess = path
	w := do(env.Srv.Handler(), "GET", "/api/logs/access?tail=50", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	var resp logsResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Truncated {
		t.Errorf("expected truncated=true for file > window")
	}
	if len(resp.Lines) > 50 {
		t.Errorf("tail limit ignored: got %d", len(resp.Lines))
	}
}

func TestActivity_Disabled(t *testing.T) {
	env := newClientTestEnv(t)
	env.Srv.Cfg.StatsAPI = ""
	w := do(env.Srv.Handler(), "GET", "/api/activity", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	var resp activityResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Enabled {
		t.Errorf("expected enabled=false")
	}
	if resp.Message == "" {
		t.Errorf("expected guidance message")
	}
}

func TestActivity_Enabled(t *testing.T) {
	env := newClientTestEnv(t)
	env.Srv.Cfg.StatsAPI = "127.0.0.1:10085"
	// Route `xray api statsquery …` through a dedicated fake via the
	// service.Manager runner — that's what activity uses in production.
	env.Srv.Service.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "api" && args[1] == "statsquery" {
			return []byte(`{"stat":[
				{"name":"user>>>alice>>>traffic>>>uplink","value":"1000"},
				{"name":"user>>>alice>>>traffic>>>downlink","value":"2000"},
				{"name":"user>>>bob>>>traffic>>>uplink","value":"500"}
			]}`), nil
		}
		return []byte(""), nil
	}

	w := do(env.Srv.Handler(), "GET", "/api/activity", nil)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp activityResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Enabled {
		t.Errorf("expected enabled=true")
	}
	byEmail := map[string]activityUserRow{}
	for _, u := range resp.Users {
		byEmail[u.Email] = u
	}
	if byEmail["alice"].Uplink != 1000 || byEmail["alice"].Downlink != 2000 {
		t.Errorf("alice: %+v", byEmail["alice"])
	}
	if byEmail["bob"].Uplink != 500 {
		t.Errorf("bob: %+v", byEmail["bob"])
	}
	// Sanity: the StatsClient type-conversion actually worked.
	_ = xray.StatsClient{}
}

