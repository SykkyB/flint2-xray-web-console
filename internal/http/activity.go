package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"time"

	"flint2-xray-web-console/internal/xray"
)

// registerActivityRoute adds GET /api/activity: per-client uplink /
// downlink totals if the stats API is wired, otherwise a stub response
// that tells the UI to show the Server tab's "Enable stats API" button.
func (s *Server) registerActivityRoute(mux *nethttp.ServeMux) {
	mux.HandleFunc("GET /api/activity", s.handleActivity)
}

type activityResp struct {
	Enabled bool              `json:"enabled"`
	Users   []activityUserRow `json:"users"`
	Message string            `json:"message,omitempty"`
}

type activityUserRow struct {
	Email    string `json:"email"`
	Uplink   int64  `json:"uplink"`
	Downlink int64  `json:"downlink"`
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
	rows := make([]activityUserRow, 0, len(stats))
	for _, u := range stats {
		rows = append(rows, activityUserRow{
			Email:    u.Email,
			Uplink:   u.Uplink,
			Downlink: u.Downlink,
		})
	}
	writeJSON(w, nethttp.StatusOK, activityResp{Enabled: true, Users: rows})
}
