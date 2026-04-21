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
