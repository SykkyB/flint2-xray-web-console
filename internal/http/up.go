package http

import (
	"context"
	nethttp "net/http"
	"time"
)

// pixelPNG is a 1×1 fully-transparent PNG, pre-encoded. The bytes
// don't matter to the consumer — the consumer is the GL.iNet stock
// home page polling us via <img>; it cares only about onload vs
// onerror to decide whether to flip the sidebar dot green.
var pixelPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

// handleUpPing is the public, no-auth, no-CORS-fuss probe used by the
// GL.iNet sidebar launcher to flip the dot next to "XRAY server"
// green when the xray service is running.
//
// 200 + 1×1 transparent PNG when `/etc/init.d/xray status` reports
// running; 404 otherwise. Two states the browser easily distinguishes
// via <img>'s onload / onerror handlers — no CORS preflight, no
// credentials, just the lightest possible cross-origin signal.
//
// Deliberately bypasses BasicAuth: the only information leaked is
// "xray up / down", which any LAN client could equally infer by
// poking the xray inbound port. Avoiding auth here also avoids the
// browser dialog that would otherwise pop on the GL.iNet UI.
func (s *Server) handleUpPing(w nethttp.ResponseWriter, r *nethttp.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	st, err := s.Service.Status(ctx)

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")

	if err != nil || !st.Running {
		nethttp.Error(w, "down", nethttp.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(pixelPNG)
}
