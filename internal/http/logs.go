package http

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	nethttp "net/http"
	"os"
	"strconv"
	"strings"
)

// registerLogRoutes wires GET /api/logs/{which} where which is "error"
// or "access". The ?tail=N query param bounds how many trailing lines
// we return (default 200, max 5000).
func (s *Server) registerLogRoutes(mux *nethttp.ServeMux) {
	mux.HandleFunc("GET /api/logs/{which}", s.handleLogs)
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
