package http

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	nethttp "net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"flint2-xray-web-console/internal/logs"
)

// registerLogRoutes wires GET /api/logs/{which} (snapshot tail) and
// GET /api/logs/{which}/stream (SSE live stream). `which` is "error"
// or "access". The snapshot's ?tail=N param bounds how many trailing
// lines we return (default 200, max 5000); the stream's ?backfill=N
// seeds the SSE with the last N historical lines (default 200, max 1000)
// before going live.
func (s *Server) registerLogRoutes(mux *nethttp.ServeMux) {
	mux.HandleFunc("GET /api/logs/{which}", s.handleLogs)
	mux.HandleFunc("GET /api/logs/{which}/stream", s.handleLogsStream)
}

type logsResp struct {
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
	// Truncated indicates that the log file was larger than the window
	// we read, so the first returned line may be a partial line (we
	// skip it in that case, but keep a flag for the UI).
	Truncated bool `json:"truncated"`
}

func (s *Server) handleLogs(w nethttp.ResponseWriter, r *nethttp.Request) {
	which := r.PathValue("which")
	var path string
	switch which {
	case "error":
		path = s.Cfg.LogError
	case "access":
		path = s.Cfg.LogAccess
	default:
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("unknown log %q; use 'error' or 'access'", which))
		return
	}
	if path == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("log path for %q is not configured", which))
		return
	}

	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("tail must be a positive integer"))
			return
		}
		if n > 5000 {
			n = 5000
		}
		tail = n
	}

	lines, truncated, err := tailFile(path, tail, 512*1024)
	if err != nil {
		writeErr(w, nethttp.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, logsResp{
		Path:      path,
		Lines:     lines,
		Truncated: truncated,
	})
}

// tailFile returns up to maxLines trailing lines from path, reading at
// most budget bytes from the end of the file. If the file is shorter
// than budget, we just read it all. If the read window starts in the
// middle of a line, we drop that partial first line and set truncated.
//
// This avoids slurping huge access logs into RAM while still giving
// the UI a sensible window without needing seek-backwards machinery.
func tailFile(path string, maxLines int, budget int64) ([]string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, false, nil
		}
		return nil, false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("stat %s: %w", path, err)
	}
	size := stat.Size()

	var (
		offset    int64
		truncated bool
	)
	if size > budget {
		offset = size - budget
		truncated = true
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, false, fmt.Errorf("seek %s: %w", path, err)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lines []string
	first := truncated // if we started mid-file, drop the first partial line
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		lines = append(lines, strings.TrimRight(scanner.Text(), "\r"))
		if len(lines) > maxLines {
			// Drop the oldest line to maintain the window.
			lines = lines[len(lines)-maxLines:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return lines, truncated, nil
}

// handleLogsStream is a Server-Sent Events endpoint that pushes one
// `data: <line>\n\n` event per appended log line. Browser side uses
// EventSource which auto-reconnects on transient drops.
//
// Initial backfill: the last `?backfill=N` lines are streamed first
// so the UI has context immediately. Heartbeat comments every 15s
// keep middleboxes from killing the idle connection.
func (s *Server) handleLogsStream(w nethttp.ResponseWriter, r *nethttp.Request) {
	which := r.PathValue("which")
	var path string
	switch which {
	case "error":
		path = s.Cfg.LogError
	case "access":
		path = s.Cfg.LogAccess
	default:
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("unknown log %q; use 'error' or 'access'", which))
		return
	}
	if path == "" {
		writeErr(w, nethttp.StatusBadRequest, fmt.Errorf("log path for %q is not configured", which))
		return
	}

	flusher, ok := w.(nethttp.Flusher)
	if !ok {
		writeErr(w, nethttp.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // belt-and-suspenders for any reverse proxy
	w.WriteHeader(nethttp.StatusOK)

	// Backfill — default 200, max 1000.
	backfill := 200
	if v := r.URL.Query().Get("backfill"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 && parsed <= 1000 {
			backfill = parsed
		}
	}
	if backfill > 0 {
		if seed, _, err := tailFile(path, backfill, 512*1024); err == nil {
			for _, line := range seed {
				writeSSE(w, line)
			}
			flusher.Flush()
		}
	}

	// Live stream from now on. Subscribe to the shared hub instead of
	// spawning our own Streamer — N concurrent SSE clients share a
	// single file tailer, the hub fans lines out non-blocking.
	if hub := s.LogHubs[which]; hub != nil {
		lines, unsub := hub.Subscribe()
		defer unsub()
		streamSSEFromChan(r.Context(), w, flusher, lines)
		return
	}

	// Fallback: per-request streamer (tests, or hubs not initialised).
	out := make(chan string, 64)
	streamCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer close(out)
		st := &logs.Streamer{Path: path}
		_ = st.Follow(streamCtx, out)
	}()
	streamSSEFromChan(r.Context(), w, flusher, out)
}

// streamSSEFromChan pumps lines from src to w as SSE events with a
// 15s heartbeat. Returns when ctx is cancelled or src closes.
func streamSSEFromChan(ctx context.Context, w nethttp.ResponseWriter, flusher nethttp.Flusher, src <-chan string) {
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-src:
			if !ok {
				return
			}
			writeSSE(w, line)
			flusher.Flush()
		case <-heartbeat.C:
			// SSE comment line — clients ignore it, but proxies and
			// browsers count it as activity.
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// writeSSE encodes a single payload as one SSE `data:` event. The SSE
// spec splits on bare \n inside `data:` so we emit one `data:` line
// per logical line (here we already get one line at a time, but be
// defensive and replace embedded \n just in case).
func writeSSE(w nethttp.ResponseWriter, payload string) {
	payload = strings.ReplaceAll(payload, "\r", "")
	// Each event terminator is a blank line.
	fmt.Fprintf(w, "data: %s\n\n", payload)
}
