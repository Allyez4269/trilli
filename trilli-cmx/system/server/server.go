// Package server holds the HTTP server for the Trilli CMX service: the operator
// auth/API surface (/api/cmx/*), a liveness/time probe, and the embedded React
// SPA (system/web) served for all non-API routes.
package server

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"time"

	"trilli-cmx/system/admin"
	"trilli-cmx/system/catalog"
	"trilli-cmx/system/comp"
	"trilli-cmx/system/directory"
	"trilli-cmx/system/infra"
	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
	"trilli-cmx/system/reports"
	"trilli-cmx/system/revenue"
	"trilli-cmx/system/support"
	"trilli-cmx/system/web"
)

// pkg is the logging package tag for this component.
const pkg = "server"

// Config configures the HTTP server.
type Config struct {
	// Addr is the listen address, e.g. "127.0.0.1:9260".
	Addr string
	// TLSConfig, when non-nil, makes the server serve HTTPS (ListenAndServeTLS)
	// instead of plain HTTP — populated from system/tls (hot-reloading wildcard
	// cert) so CMX can terminate HTTPS in-process.
	TLSConfig *tls.Config
	// ServiceName is reported in JSON responses.
	ServiceName string
	// Operators is the operator auth handler whose routes are mounted under
	// /api/cmx. Required.
	Operators *operators.Handler
	// Directory is the Customers + Accounts read handler (SPEC §6.1/§6.2).
	Directory *directory.Handler
	// Catalog is the Plans read handler (SPEC §6.3).
	Catalog *catalog.Handler
	// Comp is the comp/ambassador handler (SPEC §6.10).
	Comp *comp.Handler
	// Revenue is the billing read handler — subscription console, invoices,
	// past-due/dunning, signup intents (SPEC §6.4). Global-only.
	Revenue *revenue.Handler
	// Support is the cross-tenant support-desk handler (SPEC §6.8).
	Support *support.Handler
	// Infra is the Infrastructure read handler — jobs, health, cost (SPEC §6.5).
	Infra *infra.Handler
	// Admin is the Administration handler — audit viewer + vault (SPEC §6.7).
	Admin *admin.Handler
	// Reports is the Reports & Marketing handler (SPEC §6.6).
	Reports *reports.Handler
}

// Server wraps the HTTP listener and its mux.
type Server struct {
	cfg  Config
	http *http.Server
}

// New builds a Server with all routes registered.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg}

	mux := http.NewServeMux()

	// Liveness / time probe.
	mux.HandleFunc("GET /api/time", s.handleTime)
	mux.HandleFunc("GET /healthz", s.handleTime)

	// Operator auth + API.
	if cfg.Operators != nil {
		cfg.Operators.Register(mux)
	}
	// Customers + Accounts read API.
	if cfg.Directory != nil {
		cfg.Directory.Register(mux)
	}
	// Catalog (Plans) read API.
	if cfg.Catalog != nil {
		cfg.Catalog.Register(mux)
	}
	// Comp / ambassador API.
	if cfg.Comp != nil {
		cfg.Comp.Register(mux)
	}
	// Revenue (billing) read API.
	if cfg.Revenue != nil {
		cfg.Revenue.Register(mux)
	}
	// Support desk API.
	if cfg.Support != nil {
		cfg.Support.Register(mux)
	}
	// Infrastructure read API.
	if cfg.Infra != nil {
		cfg.Infra.Register(mux)
	}
	// Administration API.
	if cfg.Admin != nil {
		cfg.Admin.Register(mux)
	}
	// Reports & Marketing API.
	if cfg.Reports != nil {
		cfg.Reports.Register(mux)
	}

	// SPA fallback for everything else (and unknown /api paths get a JSON 404).
	mux.Handle("/", web.SPAHandler())

	s.http = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		TLSConfig:         cfg.TLSConfig,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

// ListenAndServe starts the server and blocks until it stops. When a TLSConfig
// is set it serves HTTPS (ListenAndServeTLS); otherwise plain HTTP.
func (s *Server) ListenAndServe() error {
	if s.http.TLSConfig != nil {
		logging.Info(pkg, "%s listening on https://%s", s.cfg.ServiceName, s.cfg.Addr)
		// Cert and key come from TLSConfig.GetCertificate, so the file
		// arguments are intentionally empty.
		if err := s.http.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
	logging.Info(pkg, "%s listening on http://%s", s.cfg.ServiceName, s.cfg.Addr)
	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleTime returns the current UTC time as JSON. It doubles as a liveness
// probe — a 200 with a fresh timestamp means the daemon is up.
func (s *Server) handleTime(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	logging.Debug(pkg, "%s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{
		"service":  s.cfg.ServiceName,
		"status":   "ok",
		"utc":      now.Format(time.RFC3339Nano),
		"unix":     now.Unix(),
		"unix_ms":  now.UnixMilli(),
		"timezone": "UTC",
	})
}

// writeJSON serializes v as indented JSON with the right content type.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
