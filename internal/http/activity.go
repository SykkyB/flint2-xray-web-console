package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"sync"
	"time"

	"flint2-xray-web-console/internal/xray"
)

// activityIdleHide is how long a user may have no observed activity
// (no byte delta, not currently online) before we hide the row. xray's
// cumulative counters never shrink, so without this the Activity tab
// would grow forever until the next xray restart.
const activityIdleHide = 15 * time.Minute

// registerActivityRoute wires /api/activity (list) and
// /api/activity/reset (clear xray counters + tracker).
func (s *Server) registerActivityRoute(mux *nethttp.ServeMux) {
	mux.HandleFunc("GET /api/activity", s.handleActivity)
	mux.HandleFunc("POST /api/activity/reset", s.handleActivityReset)
}

type activityResp struct {
	Enabled       bool              `json:"enabled"`
	OnlineTracked bool              `json:"online_tracked"`
	Users         []activityUserRow `json:"users"`
	HiddenIdle    int               `json:"hidden_idle,omitempty"`
	Message       string            `json:"message,omitempty"`
}

type activityUserRow struct {
	Email    string `json:"email"`
	Uplink   int64  `json:"uplink"`
	Downlink int64  `json:"downlink"`
	Online   bool   `json:"online"`
	Sessions int    `json:"sessions"`
}

// activityTracker remembers when we last saw a user do something (a
// byte-delta, or an online session). xray's counters are cumulative
// since its last start, so we need this to age stale rows off.
type activityTracker struct {
	mu      sync.Mutex
	seen    map[string]*activityEntry
	nowFunc func() time.Time // overridable for tests
}

type activityEntry struct {
	lastBytes  int64
	lastActive time.Time
}

func newActivityTracker() *activityTracker {
	return &activityTracker{seen: map[string]*activityEntry{}, nowFunc: time.Now}
}

// observe updates the tracker with the latest (email, total bytes,
// online) triple and returns whether this user should be shown. A user
// is shown if they are online now or if their byte total moved within
// the idle-hide window.
//
// First-sight with total==0 and not online is treated as "seen but
// idle": we remember them with a zeroed lastActive so they count as
// stale until they actually do something. Otherwise pressing
// "Reset stats" would repopulate the list on the very next refresh,
// because every user's counters are now 0 but unseen by the tracker.
func (t *activityTracker) observe(email string, total int64, online bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.nowFunc()
	e, ok := t.seen[email]
	if !ok {
		e = &activityEntry{lastBytes: total}
		if total > 0 || online {
			e.lastActive = now
		}
		t.seen[email] = e
		return total > 0 || online
	}
	if online || total != e.lastBytes {
		e.lastBytes = total
		e.lastActive = now
	}
	if online {
		return true
	}
	if e.lastActive.IsZero() {
		return false
	}
	return now.Sub(e.lastActive) <= activityIdleHide
}

func (t *activityTracker) reset() {
	t.mu.Lock()
	t.seen = map[string]*activityEntry{}
	t.mu.Unlock()
}

func (s *Server) tracker() *activityTracker {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activity == nil {
		s.activity = newActivityTracker()
	}
	return s.activity
}

func (s *Server) handleActivity(w nethttp.ResponseWriter, r *nethttp.Request) {
	if s.Cfg.StatsAPI == "" {
		writeJSON(w, nethttp.StatusOK, activityResp{
			Enabled: false,
			Message: "stats_api is not configured; use POST /api/server/enable-stats to turn it on",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// service.Runner and xray.Runner share the same underlying function
	// type; the explicit conversion keeps the types distinct at the
	// package boundary while letting us reuse the Service manager's
	// executor (and its fake in tests).
	var run xray.Runner
	if s.Service.Run != nil {
		run = xray.Runner(s.Service.Run)
	}
	client := &xray.StatsClient{
		XrayBin: s.Cfg.XrayBin,
		Server:  s.Cfg.StatsAPI,
		Run:     run,
	}
	stats, err := client.QueryUsers(ctx)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("query stats: %w", err))
		return
	}
	// Online tracking is best-effort: it only works when
	// policy.levels.0.statsUserOnline is on, and may not be supported on
	// older xray builds. A failure here downgrades the response to
	// online_tracked=false rather than blowing up the whole page.
	onlineByEmail := map[string]int{}
	onlineTracked := false
	if online, err := client.QueryOnline(ctx); err == nil {
		onlineTracked = true
		for _, u := range online {
			onlineByEmail[u.Email] = u.Sessions
		}
	}
	tr := s.tracker()
	rows := make([]activityUserRow, 0, len(stats))
	hidden := 0
	for _, u := range stats {
		sessions, on := onlineByEmail[u.Email]
		isOnline := on && sessions > 0
		show := tr.observe(u.Email, u.Uplink+u.Downlink, isOnline)
		if !show {
			hidden++
			continue
		}
		rows = append(rows, activityUserRow{
			Email:    u.Email,
			Uplink:   u.Uplink,
			Downlink: u.Downlink,
			Online:   isOnline,
			Sessions: sessions,
		})
	}
	writeJSON(w, nethttp.StatusOK, activityResp{
		Enabled:       true,
		OnlineTracked: onlineTracked,
		Users:         rows,
		HiddenIdle:    hidden,
	})
}

// handleActivityReset zeroes xray's per-user stat counters (via
// `statsquery -reset`) and clears our idle tracker so the next poll
// starts from a clean slate. No xray restart needed.
func (s *Server) handleActivityReset(w nethttp.ResponseWriter, r *nethttp.Request) {
	if s.Cfg.StatsAPI == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("stats_api is not configured"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var run xray.Runner
	if s.Service.Run != nil {
		run = xray.Runner(s.Service.Run)
	}
	client := &xray.StatsClient{
		XrayBin: s.Cfg.XrayBin,
		Server:  s.Cfg.StatsAPI,
		Run:     run,
	}
	if err := client.ResetUsers(ctx); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("reset stats: %w", err))
		return
	}
	s.tracker().reset()
	writeJSON(w, nethttp.StatusOK, map[string]string{"status": "reset"})
}
