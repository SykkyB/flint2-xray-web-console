// Package vless builds vless:// connection URLs from a set of
// already-resolved parameters. It does no I/O: callers gather the bits
// from the xray config and wherever the derived public key lives, then
// ask us to stringify them.
package vless

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Params is everything needed to emit one vless://… URL for a Reality
// VLESS inbound. Unset optional fields are simply omitted from the URL.
type Params struct {
	// UUID is the client's id as it appears in xray's clients[].id.
	UUID string
	// Host is the server address clients will dial (vless:// authority).
	// Typically the WAN IP or a public hostname.
	Host string
	// Port is the VLESS inbound port.
	Port int
	// Flow is the VLESS flow control, e.g. "xtls-rprx-vision".
	Flow string
	// Name is the tag shown in VPN clients (after the # in the URL).
	Name string
	// Network is the stream layer, e.g. "tcp". Defaults to "tcp" when empty.
	Network string
	// SNI is one of realitySettings.serverNames.
	SNI string
	// Fingerprint mirrors realitySettings.fingerprint (chrome, firefox, …).
	Fingerprint string
	// PublicKey is the base64 X25519 public key derived from the server's
	// realitySettings.privateKey.
	PublicKey string
	// ShortID is one of realitySettings.shortIds.
	ShortID string
	// SpiderX is the Reality spider path (optional; xray default is "/").
	SpiderX string
}

// BuildURL renders Params as a vless:// URL. It validates the minimum set
// of required fields (uuid, host, port, public key) and returns an error
// otherwise — a silently-missing pbk would produce a URL that looks right
// but can't connect.
func BuildURL(p Params) (string, error) {
	if p.UUID == "" {
		return "", fmt.Errorf("uuid is required")
	}
	if p.Host == "" {
		return "", fmt.Errorf("host is required")
	}
	if p.Port <= 0 || p.Port > 65535 {
		return "", fmt.Errorf("port %d out of range", p.Port)
	}
	if p.PublicKey == "" {
		return "", fmt.Errorf("public key is required")
	}

	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("security", "reality")
	network := p.Network
	if network == "" {
		network = "tcp"
	}
	q.Set("type", network)
	if p.Flow != "" {
		q.Set("flow", p.Flow)
	}
	if p.SNI != "" {
		q.Set("sni", p.SNI)
	}
	if p.Fingerprint != "" {
		q.Set("fp", p.Fingerprint)
	}
	q.Set("pbk", p.PublicKey)
	if p.ShortID != "" {
		q.Set("sid", p.ShortID)
	}
	if p.SpiderX != "" {
		q.Set("spx", p.SpiderX)
	}

	authority := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	// net.JoinHostPort wraps IPv6 in []; vless:// accepts that shape, but
	// for ordinary hostnames we want host:port unbracketed, which is what
	// JoinHostPort already does.

	var b strings.Builder
	b.WriteString("vless://")
	b.WriteString(url.PathEscape(p.UUID))
	b.WriteByte('@')
	b.WriteString(authority)
	b.WriteByte('?')
	b.WriteString(q.Encode())
	if p.Name != "" {
		b.WriteByte('#')
		b.WriteString(url.PathEscape(p.Name))
	}
	return b.String(), nil
}
