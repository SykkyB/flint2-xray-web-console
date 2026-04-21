// Package config loads and validates the xray-panel runtime configuration
// (panel.yaml). It intentionally does NOT read or validate the xray config
// itself — that belongs to internal/xray.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the panel's own runtime configuration, loaded from panel.yaml.
// Everything the panel touches on the router is parameterised through here so
// nothing is hardcoded.
type Config struct {
	// Listen is the TCP address (host:port) the panel's HTTP server binds to.
	// LAN-only by default; WAN-binding is rejected at startup elsewhere.
	Listen string `yaml:"listen"`

	// ServerAddress is the hostname or IP that goes into generated vless://
	// links — what clients will connect to from the outside.
	ServerAddress string `yaml:"server_address"`

	// XrayConfig is the path to xray's config.json (e.g. /etc/xray/config.json).
	XrayConfig string `yaml:"xray_config"`

	// XrayBin is the xray binary used for shelling out to `xray x25519`,
	// `xray api statsquery`, and config validation.
	XrayBin string `yaml:"xray_bin"`

	// XrayInit is the procd init script path, used for start/stop/restart.
	XrayInit string `yaml:"xray_init"`

	// LogError / LogAccess are the xray log files surfaced by the Logs tab.
	LogError  string `yaml:"log_error"`
	LogAccess string `yaml:"log_access"`

	// StatsAPI is the host:port of xray's gRPC stats API. Empty string means
	// stats are disabled; the panel will offer an "Enable stats API" button.
	StatsAPI string `yaml:"stats_api"`

	// DisabledStore is a JSON file where disabled clients live while they are
	// not in the live xray config. Re-enabling restores the same key.
	DisabledStore string `yaml:"disabled_store"`

	Auth AuthConfig `yaml:"auth"`
}

// AuthConfig is the basic-auth credential pair. The password is stored as a
// bcrypt hash so the yaml file can be kept on the router without leaking a
// plaintext password.
type AuthConfig struct {
	Username       string `yaml:"username"`
	PasswordBcrypt string `yaml:"password_bcrypt"`
}

// Load reads, parses, and validates the panel config at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse decodes and validates yaml bytes. Split from Load to make tests easy.
func Parse(raw []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.Listen == "" {
		return fmt.Errorf("listen is required")
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if c.ServerAddress == "" {
		return fmt.Errorf("server_address is required")
	}
	for _, f := range []struct {
		name, val string
	}{
		{"xray_config", c.XrayConfig},
		{"xray_bin", c.XrayBin},
		{"xray_init", c.XrayInit},
		{"disabled_store", c.DisabledStore},
	} {
		if f.val == "" {
			return fmt.Errorf("%s is required", f.name)
		}
		if !filepath.IsAbs(f.val) {
			return fmt.Errorf("%s must be an absolute path, got %q", f.name, f.val)
		}
	}
	if c.StatsAPI != "" {
		if _, _, err := net.SplitHostPort(c.StatsAPI); err != nil {
			return fmt.Errorf("stats_api %q: %w", c.StatsAPI, err)
		}
	}
	if c.Auth.Username == "" {
		return fmt.Errorf("auth.username is required")
	}
	if c.Auth.PasswordBcrypt == "" {
		return fmt.Errorf("auth.password_bcrypt is required")
	}
	if !strings.HasPrefix(c.Auth.PasswordBcrypt, "$2") {
		return fmt.Errorf("auth.password_bcrypt does not look like a bcrypt hash")
	}
	return nil
}
