package http

import (
	"embed"
	"io/fs"
	nethttp "net/http"
)

// uiFS holds the embedded static UI. Files live alongside the http
// package because Go's embed directive can't reach upward out of its
// own package directory.
//
//go:embed web
var uiFS embed.FS

// registerUIRoutes serves the static UI at /. Any path that isn't
// handled by the /api/... routes falls through to this handler; if the
// file isn't found, we serve index.html so the (currently tiny) SPA
// can still boot from nested URLs.
func (s *Server) registerUIRoutes(mux *nethttp.ServeMux) {
	sub, err := fs.Sub(uiFS, "web")
	if err != nil {
		// Embed misconfiguration is a build-time problem; failing here
		// is fine — it'll surface during development.
		panic(err)
	}
	fileServer := nethttp.FileServer(nethttp.FS(sub))
	mux.Handle("/", spaFallback(sub, fileServer))
}

// spaFallback is a FileServer wrapper: when a request targets a path
// that doesn't exist as a file and isn't a directory, serve index.html
// so client-side routing (hash routes for now) can take over.
func spaFallback(root fs.FS, fileServer nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		path := r.URL.Path
		if path == "/" || path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		trimmed := path[1:]
		if f, err := root.Open(trimmed); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fallback: serve index.html so refresh-on-a-tab doesn't 404.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
