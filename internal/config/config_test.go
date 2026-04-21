package config

import (
	"strings"
	"testing"
)

const validYAML = `
listen: "192.168.1.1:8080"
server_address: "vpn.example.com"
xray_config: /etc/xray/config.json
xray_bin: /usr/bin/xray
xray_init: /etc/init.d/xray
log_error: /tmp/xray-error.log
log_access: /tmp/xray-access.log
stats_api: "127.0.0.1:10085"
disabled_store: /etc/xray-panel/disabled.json
auth:
  username: admin
  password_bcrypt: "$2a$10$abcdefghijklmnopqrstuv"
`

func TestParse_Valid(t *testing.T) {
	c, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Listen != "192.168.1.1:8080" {
		t.Errorf("listen: got %q", c.Listen)
	}
	if c.ServerAddress != "vpn.example.com" {
		t.Errorf("server_address: got %q", c.ServerAddress)
	}
	if c.StatsAPI != "127.0.0.1:10085" {
		t.Errorf("stats_api: got %q", c.StatsAPI)
	}
	if c.Auth.Username != "admin" {
		t.Errorf("auth.username: got %q", c.Auth.Username)
	}
}

func TestParse_StatsDisabled(t *testing.T) {
	y := strings.Replace(validYAML, `stats_api: "127.0.0.1:10085"`, `stats_api: ""`, 1)
	c, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.StatsAPI != "" {
		t.Errorf("expected empty stats_api, got %q", c.StatsAPI)
	}
}

func TestParse_Invalid(t *testing.T) {
	cases := map[string]string{
		"missing listen":         strings.Replace(validYAML, `listen: "192.168.1.1:8080"`, `listen: ""`, 1),
		"bad listen":             strings.Replace(validYAML, `listen: "192.168.1.1:8080"`, `listen: "not-a-hostport"`, 1),
		"missing server_address": strings.Replace(validYAML, `server_address: "vpn.example.com"`, `server_address: ""`, 1),
		"relative xray_config":   strings.Replace(validYAML, `xray_config: /etc/xray/config.json`, `xray_config: etc/xray/config.json`, 1),
		"bad stats_api":          strings.Replace(validYAML, `stats_api: "127.0.0.1:10085"`, `stats_api: "bogus"`, 1),
		"missing username":       strings.Replace(validYAML, `username: admin`, `username: ""`, 1),
		"plaintext password": strings.Replace(validYAML,
			`password_bcrypt: "$2a$10$abcdefghijklmnopqrstuv"`,
			`password_bcrypt: "hunter2"`, 1),
		"unknown field": validYAML + "\nextra_field: oops\n",
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(y)); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}
