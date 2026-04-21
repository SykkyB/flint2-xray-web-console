package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"time"
)

// registerServiceRoutes adds the service-control endpoints (start /
// stop / restart). GET /api/service/status is registered separately in
// Handler so both the read-only status and the action endpoints live in
// one routing table.
func (s *Server) registerServiceRoutes(mux *nethttp.ServeMux) {
	mux.HandleFunc("POST /api/service/start", s.handleServiceStart)
	mux.HandleFunc("POST /api/service/stop", s.handleServiceStop)
	mux.HandleFunc("POST /api/service/restart", s.handleServiceRestart)
}

func (s *Server) handleServiceStart(w nethttp.ResponseWriter, r *nethttp.Request) {
	s.runServiceAction(w, r, "start", s.Service.Start)
}

func (s *Server) handleServiceStop(w nethttp.ResponseWriter, r *nethttp.Request) {
	s.runServiceAction(w, r, "stop", s.Service.Stop)
}

// handleServiceRestart always validates config.json via `xray -test`
// before flipping the service. Stop/Start bypass that check because
// they don't load a (potentially broken) config into xray.
func (s *Server) handleServiceRestart(w nethttp.ResponseWriter, r *nethttp.Request) {
	s.runServiceAction(w, r, "restart", s.Service.Restart)
}

func (s *Server) runServiceAction(w nethttp.ResponseWriter, r *nethttp.Request, name string, fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := fn(ctx); err != nil {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("%s: %w", name, err))
		return
	}
	st, err := s.Service.Status(ctx)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, st)
}
