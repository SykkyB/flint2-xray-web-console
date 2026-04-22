package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// StatsClient wraps `xray api statsquery`. It shells out via the same
// Runner as KeyTool, which lets tests supply deterministic output.
type StatsClient struct {
	XrayBin string
	// Server is the host:port of xray's gRPC stats API, as advertised
	// by panel.yaml's stats_api.
	Server string
	Run    Runner
}

// UserStats is a single client's uplink/downlink byte totals since the
// last stats reset.
type UserStats struct {
	Email    string `json:"email"`
	Uplink   int64  `json:"uplink"`
	Downlink int64  `json:"downlink"`
}

// OnlineUser is one entry from `xray api statsgetallonlineusers`. It
// captures the email and the current open-session count; Xray only
// reports users that policy.levels.*.statsUserOnline tracks.
type OnlineUser struct {
	Email    string `json:"email"`
	Sessions int    `json:"sessions"`
}

// statsqueryOutput is the shape xray prints when invoked with
// `-json` (the default on recent versions). We accept both wrappers
// ("stat" and "statistics") just in case.
type statsqueryOutput struct {
	Stat       []statEntry `json:"stat"`
	Statistics []statEntry `json:"statistics"`
}

type statEntry struct {
	Name  string `json:"name"`
	Value any    `json:"value"` // string on some versions, int on others
}

// QueryUsers runs `xray api statsquery -server <addr> -pattern "user>>>"`
// and parses the result into a per-user summary. Pattern names have the
// shape "user>>>{email}>>>traffic>>>{direction}"; unmatched names are
// ignored.
func (c *StatsClient) QueryUsers(ctx context.Context) ([]UserStats, error) {
	if c.Server == "" {
		return nil, fmt.Errorf("stats server not configured")
	}
	run := c.Run
	if run == nil {
		run = DefaultRunner
	}
	out, err := run(ctx, c.XrayBin, "api", "statsquery", "-server", c.Server, "-pattern", "user>>>")
	if err != nil {
		return nil, fmt.Errorf("xray api statsquery: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseStats(out)
}

// QueryOnline runs `xray api statsgetallonlineusers --server=<addr>`
// and returns the list of users currently holding at least one open
// session. Requires policy.levels.*.statsUserOnline = true in the xray
// config; without it xray will return an empty object for every user.
func (c *StatsClient) QueryOnline(ctx context.Context) ([]OnlineUser, error) {
	if c.Server == "" {
		return nil, fmt.Errorf("stats server not configured")
	}
	run := c.Run
	if run == nil {
		run = DefaultRunner
	}
	out, err := run(ctx, c.XrayBin, "api", "statsgetallonlineusers", "--server="+c.Server)
	if err != nil {
		return nil, fmt.Errorf("xray api statsgetallonlineusers: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseOnlineUsers(out)
}

// parseOnlineUsers accepts every shape xray is known to emit:
//   - flat map:     `{"alice": 1, "bob": 2}`
//   - object:       `{"users": {"alice": 1}}`
//   - array-of-row: `{"users":[{"email":"alice","count":1}]}`
//   - array-of-str: `{"users":["user>>>alice>>>online", ...]}`  (xray v26)
//
// The string form has no session count — each occurrence counts as one
// session, and duplicates are summed. Numeric values may be strings or
// ints depending on xray version. An empty object means "no one online".
func parseOnlineUsers(raw []byte) ([]OnlineUser, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "{}" {
		return nil, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("parse online users json: %w (output: %s)", err, string(raw))
	}
	if inner, ok := probe["users"]; ok && len(probe) == 1 {
		// Try object-of-emails shape first.
		var m map[string]json.RawMessage
		if err := json.Unmarshal(inner, &m); err == nil {
			return buildOnlineFromMap(m), nil
		}
		// Array-of-strings (xray v26): "user>>>{email}>>>online".
		var strs []string
		if err := json.Unmarshal(inner, &strs); err == nil {
			counts := map[string]int{}
			order := []string{}
			for _, s := range strs {
				email := extractOnlineEmail(s)
				if email == "" {
					continue
				}
				if _, seen := counts[email]; !seen {
					order = append(order, email)
				}
				counts[email]++
			}
			out := make([]OnlineUser, 0, len(order))
			for _, e := range order {
				out = append(out, OnlineUser{Email: e, Sessions: counts[e]})
			}
			return out, nil
		}
		// Array of {email, count} rows.
		var rows []struct {
			Email string `json:"email"`
			Count any    `json:"count"`
		}
		if err := json.Unmarshal(inner, &rows); err == nil {
			out := make([]OnlineUser, 0, len(rows))
			for _, r := range rows {
				if r.Email == "" {
					continue
				}
				out = append(out, OnlineUser{Email: r.Email, Sessions: int(coerceInt(r.Count))})
			}
			return out, nil
		}
		return nil, fmt.Errorf("parse online users json: unrecognised 'users' shape: %s", string(inner))
	}
	return buildOnlineFromMap(probe), nil
}

// extractOnlineEmail pulls "alice" out of "user>>>alice>>>online". Emails
// themselves never contain ">>>" in practice, so splitting on that is
// safe; any other shape yields "" and is skipped by the caller.
func extractOnlineEmail(s string) string {
	const sep = ">>>"
	parts := strings.Split(s, sep)
	if len(parts) < 2 || parts[0] != "user" {
		return ""
	}
	return parts[1]
}

func buildOnlineFromMap(m map[string]json.RawMessage) []OnlineUser {
	out := make([]OnlineUser, 0, len(m))
	for email, raw := range m {
		var v any
		_ = json.Unmarshal(raw, &v)
		out = append(out, OnlineUser{Email: email, Sessions: int(coerceInt(v))})
	}
	return out
}

func parseStats(raw []byte) ([]UserStats, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, nil
	}
	var wrap statsqueryOutput
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, fmt.Errorf("parse stats json: %w (output: %s)", err, string(raw))
	}
	entries := wrap.Stat
	if len(entries) == 0 {
		entries = wrap.Statistics
	}

	byEmail := map[string]*UserStats{}
	for _, e := range entries {
		email, direction, ok := parseUserStatName(e.Name)
		if !ok {
			continue
		}
		val := coerceInt(e.Value)
		u, exists := byEmail[email]
		if !exists {
			u = &UserStats{Email: email}
			byEmail[email] = u
		}
		switch direction {
		case "uplink":
			u.Uplink = val
		case "downlink":
			u.Downlink = val
		}
	}
	result := make([]UserStats, 0, len(byEmail))
	for _, u := range byEmail {
		result = append(result, *u)
	}
	return result, nil
}

// parseUserStatName extracts ("alice", "uplink", true) from a stat name
// like "user>>>alice>>>traffic>>>uplink". Emails may contain ">" in
// theory, so we only split on the outer ">>>" separator. Anything that
// doesn't match the exact shape is skipped.
func parseUserStatName(name string) (email, direction string, ok bool) {
	const sep = ">>>"
	parts := strings.Split(name, sep)
	if len(parts) != 4 {
		return "", "", false
	}
	if parts[0] != "user" || parts[2] != "traffic" {
		return "", "", false
	}
	return parts[1], parts[3], true
}

func coerceInt(v any) int64 {
	switch t := v.(type) {
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	case float64:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	default:
		return 0
	}
}
