package vless

import (
	"net/url"
	"strings"
	"testing"
)

func baseParams() Params {
	return Params{
		UUID:        "11111111-1111-1111-1111-111111111111",
		Host:        "vpn.example.com",
		Port:        9443,
		Flow:        "xtls-rprx-vision",
		Name:        "alice",
		Network:     "tcp",
		SNI:         "www.cloudflare.com",
		Fingerprint: "chrome",
		PublicKey:   "PUBKEY_BASE64",
		ShortID:     "deadbeef",
	}
}

func parseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}

func TestBuildURL_Shape(t *testing.T) {
	got, err := BuildURL(baseParams())
	if err != nil {
		t.Fatalf("BuildURL: %v", err)
	}
	u := parseURL(t, got)
	if u.Scheme != "vless" {
		t.Errorf("scheme: %q", u.Scheme)
	}
	if u.User.Username() != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("user: %q", u.User.Username())
	}
	if u.Host != "vpn.example.com:9443" {
		t.Errorf("host: %q", u.Host)
	}
	if u.Fragment != "alice" {
		t.Errorf("fragment: %q", u.Fragment)
	}
	q := u.Query()
	want := map[string]string{
		"encryption": "none",
		"security":   "reality",
		"type":       "tcp",
		"flow":       "xtls-rprx-vision",
		"sni":        "www.cloudflare.com",
		"fp":         "chrome",
		"pbk":        "PUBKEY_BASE64",
		"sid":        "deadbeef",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s: got %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Has("spx") {
		t.Errorf("spx should be absent when unset")
	}
}

func TestBuildURL_NetworkDefaultsTCP(t *testing.T) {
	p := baseParams()
	p.Network = ""
	got, err := BuildURL(p)
	if err != nil {
		t.Fatalf("BuildURL: %v", err)
	}
	if !strings.Contains(got, "type=tcp") {
		t.Errorf("expected type=tcp, got %q", got)
	}
}

func TestBuildURL_EncodesNameWithSpaces(t *testing.T) {
	p := baseParams()
	p.Name = "my phone"
	got, err := BuildURL(p)
	if err != nil {
		t.Fatalf("BuildURL: %v", err)
	}
	// path-escaped: %20 or + depending on encoder; url.PathEscape uses %20
	if !strings.HasSuffix(got, "#my%20phone") {
		t.Errorf("name not encoded as expected: %q", got)
	}
}

func TestBuildURL_Validation(t *testing.T) {
	cases := map[string]func(*Params){
		"missing uuid": func(p *Params) { p.UUID = "" },
		"missing host": func(p *Params) { p.Host = "" },
		"bad port hi":  func(p *Params) { p.Port = 70000 },
		"bad port lo":  func(p *Params) { p.Port = 0 },
		"missing pbk":  func(p *Params) { p.PublicKey = "" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			p := baseParams()
			mut(&p)
			if _, err := BuildURL(p); err == nil {
				t.Errorf("expected error")
			}
		})
	}
}
