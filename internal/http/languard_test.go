package http

import (
	"strings"
	"testing"
)

func TestCheckLANBind_Accepts(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:8080",
		"192.168.1.1:8080",
		"10.0.0.5:80",
		"172.16.0.1:9999",
		"[::1]:8080",
		"[fe80::1]:8080",
		"[fd00::1]:8080",
	} {
		t.Run(addr, func(t *testing.T) {
			if err := CheckLANBind(addr); err != nil {
				t.Errorf("CheckLANBind(%q): unexpected error %v", addr, err)
			}
		})
	}
}

func TestCheckLANBind_Rejects(t *testing.T) {
	cases := map[string]string{
		"wildcard v4":     "0.0.0.0:8080",
		"wildcard v6":     "[::]:8080",
		"public IPv4":     "8.8.8.8:8080",
		"public IPv6":     "[2606:4700::1]:8080",
		"missing port":    "192.168.1.1",
		"empty":           "",
		"bad listen":      "not a hostport",
	}
	for name, addr := range cases {
		t.Run(name, func(t *testing.T) {
			err := CheckLANBind(addr)
			if err == nil {
				t.Errorf("CheckLANBind(%q): expected error, got nil", addr)
				return
			}
			if strings.TrimSpace(err.Error()) == "" {
				t.Errorf("error has empty message")
			}
		})
	}
}
