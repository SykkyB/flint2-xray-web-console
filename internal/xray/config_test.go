package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Real-shape fixture matching the router's current config.json (values
// redacted). Unknown-to-us fields like "futureField" and "customBlock" are
// sprinkled in to prove round-trip preservation.
const fixtureJSON = `{
  "log": { "access": "/tmp/xray-access.log", "error": "/tmp/xray-error.log", "loglevel": "warning" },
  "customBlock": { "hello": "world" },
  "inbounds": [{
    "listen": "0.0.0.0",
    "port": 9443,
    "protocol": "vless",
    "futureField": 42,
    "settings": {
      "decryption": "none",
      "clients": [
        { "id": "11111111-1111-1111-1111-111111111111", "flow": "xtls-rprx-vision" },
        { "id": "22222222-2222-2222-2222-222222222222", "flow": "xtls-rprx-vision", "email": "alice" }
      ]
    },
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "show": false,
        "dest": "www.cloudflare.com:443",
        "xver": 0,
        "serverNames": ["www.cloudflare.com"],
        "privateKey": "REDACTED_PRIV",
        "shortIds": ["deadbeef"],
        "fingerprint": "chrome",
        "mystery": true
      },
      "tlsSettings": { "alpn": ["h2", "http/1.1"] }
    }
  }],
  "outbounds": [{ "protocol": "freedom", "tag": "direct" }]
}`

func TestRoundTripPreservesUnknownFields(t *testing.T) {
	var f File
	if err := json.Unmarshal([]byte(fixtureJSON), &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out, err := Marshal(&f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Semantic round-trip: parse both and compare as maps.
	var a, b map[string]any
	if err := json.Unmarshal([]byte(fixtureJSON), &a); err != nil {
		t.Fatalf("reparse fixture: %v", err)
	}
	if err := json.Unmarshal(out, &b); err != nil {
		t.Fatalf("reparse output: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("round-trip mismatch.\nfixture: %s\noutput: %s", mustMarshal(a), mustMarshal(b))
	}
}

func TestFindAndRemoveClient(t *testing.T) {
	var f File
	if err := json.Unmarshal([]byte(fixtureJSON), &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got := f.FindClient("22222222-2222-2222-2222-222222222222"); got == nil || got.Email != "alice" {
		t.Fatalf("FindClient alice: got %+v", got)
	}
	if got := f.FindClient("does-not-exist"); got != nil {
		t.Fatalf("FindClient: expected nil, got %+v", got)
	}

	removed, err := f.RemoveClient("11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("RemoveClient: %v", err)
	}
	if removed.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("removed.ID: got %q", removed.ID)
	}

	if _, err := f.RemoveClient("11111111-1111-1111-1111-111111111111"); err == nil {
		t.Errorf("expected error removing already-removed client")
	}

	in, _ := f.PrimaryInbound()
	if len(in.Settings.Clients) != 1 {
		t.Errorf("clients after remove: got %d, want 1", len(in.Settings.Clients))
	}
}

func TestAddClient(t *testing.T) {
	var f File
	if err := json.Unmarshal([]byte(fixtureJSON), &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	c, err := f.AddClient("bob", "xtls-rprx-vision")
	if err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	if c.ID == "" || len(c.ID) != 36 {
		t.Errorf("AddClient: unexpected UUID %q", c.ID)
	}
	if c.Email != "bob" {
		t.Errorf("AddClient: email=%q", c.Email)
	}
	if got := f.FindClient(c.ID); got == nil {
		t.Errorf("AddClient: freshly added client not findable")
	}

	// Duplicate name must be rejected.
	if _, err := f.AddClient("bob", "xtls-rprx-vision"); err == nil {
		t.Errorf("expected duplicate-name error")
	}
}

func TestWriteAtomicAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Seed existing config; Write should produce a .bak of it.
	if err := os.WriteFile(path, []byte(fixtureJSON), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := f.AddClient("carol", "xtls-rprx-vision"); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	if err := Write(path, f); err != nil {
		t.Fatalf("Write: %v", err)
	}

	bakBytes, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("expected .bak file: %v", err)
	}
	var bak map[string]any
	if err := json.Unmarshal(bakBytes, &bak); err != nil {
		t.Fatalf("parse .bak: %v", err)
	}
	var original map[string]any
	_ = json.Unmarshal([]byte(fixtureJSON), &original)
	if !reflect.DeepEqual(bak, original) {
		t.Errorf(".bak does not match original config")
	}

	// Reread and confirm carol survived and unknown fields still present.
	reread, err := Read(path)
	if err != nil {
		t.Fatalf("re-Read: %v", err)
	}
	found := false
	in, _ := reread.PrimaryInbound()
	for _, c := range in.Settings.Clients {
		if c.Email == "carol" {
			found = true
		}
	}
	if !found {
		t.Errorf("carol not present after write+read")
	}

	rereadBytes, _ := os.ReadFile(path)
	var rereadMap map[string]any
	_ = json.Unmarshal(rereadBytes, &rereadMap)
	if _, ok := rereadMap["customBlock"]; !ok {
		t.Errorf("unknown top-level field customBlock dropped on rewrite")
	}

	// No stray tmp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

func mustMarshal(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
