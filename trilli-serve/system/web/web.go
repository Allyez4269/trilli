// Package web embeds the Trilli SPA bundle (built by Vite from interface/) and
// exposes an http.Handler that serves it with SPA fallback for client-side
// routing. The entire frontend ships inside the Go binary — no Node, Vite, or
// dist/ directory is needed at runtime.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// FS returns a filesystem rooted at the built SPA directory (dist/).
func FS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// Handler serves the embedded SPA bundle with index.html fallback for any
// unknown path, so React Router can own client-side routing.
//
// Cache strategy:
//   - index.html: no-cache so the browser always re-validates and picks up
//     new bundle hashes on every page load. Without this, a stale cached
//     index.html keeps pointing at an old hashed bundle (which 404s after
//     a deploy) and the SPA appears broken.
//   - everything under /assets/ (Vite hash-named): public, immutable,
//     1-year max-age — the filename changes on every build so it's safe
//     to cache forever.
func Handler() (http.Handler, error) {
	sub, err := FS()
	if err != nil {
		return nil, err
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
		// Share + drop-portal landing pages (/s/<token>, /p/<token>) are private,
		// unlisted, token-gated links — often password-protected secure downloads.
		// Keep them out of search engines regardless of which path serves the HTML.
		if clean == "s" || clean == "p" || clean == "sign" || strings.HasPrefix(clean, "s/") || strings.HasPrefix(clean, "p/") || strings.HasPrefix(clean, "sign/") {
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
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

		// SPA fallback — return index.html for unknown paths.
		serveIndex(w, r)
	}), nil
}
