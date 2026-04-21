// Package xray reads and writes the xray config.json file.
//
// The on-disk config may contain arbitrary fields we don't know about; the
// round-trip must preserve them verbatim. This file therefore models only
// the pieces the panel edits (inbound[0].settings.clients and
// inbound[0].streamSettings.realitySettings) and keeps everything else as
// json.RawMessage so we never drop a field we didn't anticipate.
package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// File is a round-trip view of /etc/xray/config.json. Unknown top-level
// fields are preserved in Extra.
type File struct {
	Log       json.RawMessage   `json:"log,omitempty"`
	DNS       json.RawMessage   `json:"dns,omitempty"`
	Routing   json.RawMessage   `json:"routing,omitempty"`
	Policy    json.RawMessage   `json:"policy,omitempty"`
	API       json.RawMessage   `json:"api,omitempty"`
	Stats     json.RawMessage   `json:"stats,omitempty"`
	Inbounds  []Inbound         `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// Inbound is a single entry in the inbounds array. We care about its VLESS
// client list and its Reality settings; everything else we leave opaque.
type Inbound struct {
	Listen         string          `json:"listen,omitempty"`
	Port           json.RawMessage `json:"port,omitempty"`
	Protocol       string          `json:"protocol,omitempty"`
	Tag            string          `json:"tag,omitempty"`
	Settings       *Settings       `json:"settings,omitempty"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
	Sniffing       json.RawMessage `json:"sniffing,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// Settings holds inbound protocol settings. For VLESS we expose Clients and
// Decryption; other protocols (or unknown fields) remain in Extra.
type Settings struct {
	Decryption string   `json:"decryption,omitempty"`
	Clients    []Client `json:"clients,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// Client is one VLESS client. Email is used by xray as the human-readable
// identifier for stats and logs; we treat it as the client "name".
type Client struct {
	ID    string `json:"id"`
	Flow  string `json:"flow,omitempty"`
	Email string `json:"email,omitempty"`
	Level *int   `json:"level,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// StreamSettings is the stream layer: network, security, and Reality
// parameters. Unknown sub-objects are preserved in Extra.
type StreamSettings struct {
	Network         string           `json:"network,omitempty"`
	Security        string           `json:"security,omitempty"`
	RealitySettings *RealitySettings `json:"realitySettings,omitempty"`
	TLSSettings     json.RawMessage  `json:"tlsSettings,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// RealitySettings mirrors xray's realitySettings block. PrivateKey is kept
// server-side; the panel never leaks it to the UI.
type RealitySettings struct {
	Show        bool     `json:"show"`
	Dest        string   `json:"dest,omitempty"`
	Xver        *int     `json:"xver,omitempty"`
	ServerNames []string `json:"serverNames,omitempty"`
	PrivateKey  string   `json:"privateKey,omitempty"`
	ShortIDs    []string `json:"shortIds,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// The custom marshal/unmarshal pairs below implement "known fields plus a
// catch-all bag" so we never drop anything on a rewrite. The pattern: decode
// into a map[string]json.RawMessage, pull out known keys into typed fields,
// keep the rest in Extra. Re-marshal in a stable order: known fields first,
// then Extra keys alphabetically.

func (f *File) UnmarshalJSON(b []byte) error {
	type known File
	aux := struct{ *known }{known: (*known)(f)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	extra, err := collectExtra(b, []string{
		"log", "dns", "routing", "policy", "api", "stats", "inbounds", "outbounds",
	})
	if err != nil {
		return err
	}
	f.Extra = extra
	return nil
}

func (f File) MarshalJSON() ([]byte, error) {
	return mergeExtra(f, f.Extra, func(enc *json.Encoder) error {
		type known File
		k := known(f)
		k.Extra = nil
		return enc.Encode(k)
	})
}

func (i *Inbound) UnmarshalJSON(b []byte) error {
	type known Inbound
	aux := struct{ *known }{known: (*known)(i)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	extra, err := collectExtra(b, []string{
		"listen", "port", "protocol", "tag", "settings", "streamSettings", "sniffing",
	})
	if err != nil {
		return err
	}
	i.Extra = extra
	return nil
}

func (i Inbound) MarshalJSON() ([]byte, error) {
	return mergeExtra(i, i.Extra, func(enc *json.Encoder) error {
		type known Inbound
		k := known(i)
		k.Extra = nil
		return enc.Encode(k)
	})
}

func (s *Settings) UnmarshalJSON(b []byte) error {
	type known Settings
	aux := struct{ *known }{known: (*known)(s)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	extra, err := collectExtra(b, []string{"decryption", "clients"})
	if err != nil {
		return err
	}
	s.Extra = extra
	return nil
}

func (s Settings) MarshalJSON() ([]byte, error) {
	return mergeExtra(s, s.Extra, func(enc *json.Encoder) error {
		type known Settings
		k := known(s)
		k.Extra = nil
		return enc.Encode(k)
	})
}

func (c *Client) UnmarshalJSON(b []byte) error {
	type known Client
	aux := struct{ *known }{known: (*known)(c)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	extra, err := collectExtra(b, []string{"id", "flow", "email", "level"})
	if err != nil {
		return err
	}
	c.Extra = extra
	return nil
}

func (c Client) MarshalJSON() ([]byte, error) {
	return mergeExtra(c, c.Extra, func(enc *json.Encoder) error {
		type known Client
		k := known(c)
		k.Extra = nil
		return enc.Encode(k)
	})
}

func (s *StreamSettings) UnmarshalJSON(b []byte) error {
	type known StreamSettings
	aux := struct{ *known }{known: (*known)(s)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	extra, err := collectExtra(b, []string{"network", "security", "realitySettings", "tlsSettings"})
	if err != nil {
		return err
	}
	s.Extra = extra
	return nil
}

func (s StreamSettings) MarshalJSON() ([]byte, error) {
	return mergeExtra(s, s.Extra, func(enc *json.Encoder) error {
		type known StreamSettings
		k := known(s)
		k.Extra = nil
		return enc.Encode(k)
	})
}

func (r *RealitySettings) UnmarshalJSON(b []byte) error {
	type known RealitySettings
	aux := struct{ *known }{known: (*known)(r)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	extra, err := collectExtra(b, []string{
		"show", "dest", "xver", "serverNames", "privateKey", "shortIds", "fingerprint",
	})
	if err != nil {
		return err
	}
	r.Extra = extra
	return nil
}

func (r RealitySettings) MarshalJSON() ([]byte, error) {
	return mergeExtra(r, r.Extra, func(enc *json.Encoder) error {
		type known RealitySettings
		k := known(r)
		k.Extra = nil
		return enc.Encode(k)
	})
}

// collectExtra returns the subset of the top-level object in raw that is not
// already represented by the listed known keys.
func collectExtra(raw []byte, knownKeys []string) (map[string]json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		// Not an object (e.g. null): nothing to preserve.
		return nil, nil
	}
	known := make(map[string]struct{}, len(knownKeys))
	for _, k := range knownKeys {
		known[k] = struct{}{}
	}
	for k := range all {
		if _, ok := known[k]; ok {
			delete(all, k)
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}

// mergeExtra re-encodes a struct's known fields via writeKnown, then splices
// the Extra bag back in. It keeps known fields first (their order comes from
// the struct definition) and appends Extra keys sorted alphabetically.
func mergeExtra(_ any, extra map[string]json.RawMessage, writeKnown func(*json.Encoder) error) ([]byte, error) {
	var knownBuf bytes.Buffer
	enc := json.NewEncoder(&knownBuf)
	enc.SetEscapeHTML(false)
	if err := writeKnown(enc); err != nil {
		return nil, err
	}
	// json.Encoder appends a newline; strip it.
	knownBytes := bytes.TrimRight(knownBuf.Bytes(), "\n")

	if len(extra) == 0 {
		return knownBytes, nil
	}

	// Decode known fields back into an ordered slice we can splice with.
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(knownBytes, &asMap); err != nil {
		return nil, fmt.Errorf("mergeExtra: reparse known fields: %w", err)
	}
	for k, v := range extra {
		if _, clash := asMap[k]; clash {
			// Known field wins; extra is only for fields we didn't model.
			continue
		}
		asMap[k] = v
	}
	// Stable order: re-emit with json.Marshal of the map gives alphabetical,
	// which is fine — xray doesn't care about field order.
	return json.Marshal(asMap)
}

// Read loads and parses the xray config at path.
func Read(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &f, nil
}

// Write serialises f and writes it atomically to path: encode to a tmp file
// in the same directory, fsync, rename over the target. Before the rename we
// keep the previous contents at path+".bak" so a bad rewrite is recoverable.
// The caller is responsible for validating that xray will actually accept
// the new config (e.g. via `xray -test`) — this function only verifies that
// the bytes we wrote round-trip through the JSON parser.
func Write(path string, f *File) error {
	out, err := Marshal(f)
	if err != nil {
		return err
	}
	// Sanity: reparse to catch programmer errors before we touch the file.
	var probe File
	if err := json.Unmarshal(out, &probe); err != nil {
		return fmt.Errorf("refusing to write unparseable config: %w", err)
	}

	if err := backupIfExists(path); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dirOf(path), ".xray-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// If we return before rename, clean up the tmp file.
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename over target: %w", err)
	}
	return nil
}

// Marshal returns the canonical bytes for f (pretty-printed, 2-space indent).
func Marshal(f *File) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func backupIfExists(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read existing for backup: %w", err)
	}
	if err := os.WriteFile(path+".bak", src, 0o600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	return nil
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
