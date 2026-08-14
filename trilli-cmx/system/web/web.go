// Package web embeds the CMX SPA bundle (built by Vite from interface/) and
// serves it with index.html fallback for client-side routing. The whole
// frontend ships inside the Go binary — no Node/Vite/dist needed at runtime.
//
// Until the real Vite build lands, dist/ holds a placeholder index.html so the
// go:embed directive compiles. Any non-API route falls through to index.html.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"trilli-cmx/system/logging"
)

//go:embed all:dist
var distFS embed.FS

// FS returns a filesystem rooted at the built SPA directory (dist/).
func FS() (fs.FS, error) { return fs.Sub(distFS, "dist") }

// SPAHandler serves the embedded SPA with index.html fallback. Unknown /api
// paths return a JSON 404 (so a stray API call never silently returns HTML).
func SPAHandler() http.Handler {
	sub, err := FS()
	if err != nil {
		logging.Error("web", "failed to open embedded SPA fs: %v", err)
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))

	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")

		// Never serve the SPA for API paths — that's a routing bug; be explicit.
		if strings.HasPrefix(clean, "api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}

		if clean == "" || clean == "index.html" {
			serveIndex(w, r)
			return
		}
		if strings.Contains(clean, "..") {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if _, err := fs.Stat(sub, clean); err == nil {
			if strings.HasPrefix(clean, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r) // SPA fallback
	})
}
