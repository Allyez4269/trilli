package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"trilli/system/logging"
)

const (
	packageName = "server"
)

// Connection constants
const (
	defaultHost = "0.0.0.0"
	defaultPort = 8081
	// ReadTimeout / WriteTimeout are 0 (no limit) on purpose: they apply to the
	// WHOLE request body read and the WHOLE response write, so any non-zero value
	// caps how long an upload or download may take. A multi-GB upload (encrypt +
	// blob-store write) or download easily exceeds tens of seconds, so a finite
	// value silently kills large transfers. Slow-loris protection on the request
	// line/headers is kept via ReadHeaderTimeout; idle keep-alives via IdleTimeout.
	defaultReadTimeout       = 0
	defaultReadHeaderTimeout = 30 * time.Second
	defaultWriteTimeout      = 0
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
)

// Config holds the configuration for the HTTP server
type Config struct {
	Host              string
	Port              int
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration

	// TLSConfig, when non-nil, makes the server serve HTTPS (ListenAndServeTLS)
	// instead of plain HTTP. Certificates are supplied via the config — typically
	// from system/tls, which hot-reloads the wildcard cert on renewal. Nil keeps
	// the legacy plain-HTTP behaviour (TLS terminated upstream by nginx).
	TLSConfig *tls.Config
}

// DefaultConfig returns a Config with default values for the HTTP server
func DefaultConfig() *Config {
	return &Config{
		Host:              defaultHost,
		Port:              defaultPort,
		ReadTimeout:       defaultReadTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
	}
}

// Server represents an HTTP server
type Server struct {
	cfg    *Config
	server *http.Server
	mux    *http.ServeMux
}

// NewServer creates a new HTTP server with the provided configuration
func NewServer(cfg *Config) *Server {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	mux := http.NewServeMux()
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	s := &Server{
		cfg: cfg,
		mux: mux,
		server: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			TLSConfig:         cfg.TLSConfig,
		},
	}

	s.registerRoutes()

	logging.Debug(packageName, "HTTP server created on %s with timeouts: read=%v, write=%v, idle=%v",
		addr, cfg.ReadTimeout, cfg.WriteTimeout, cfg.IdleTimeout)

	return s
}

// registerRoutes wires up the default route set
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/time", s.handleAPITime)
}

// Handle registers a handler for the given pattern on the server's mux
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

// HandleFunc registers a handler function for the given pattern on the server's mux
func (s *Server) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	s.mux.HandleFunc(pattern, handler)
}

// Mux returns the server's mux so callers can mount a SECOND listener (e.g. a
// loopback plain-HTTP server for internal WOPI traffic) that shares the same
// route set as the public TLS server.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// Start starts the server and blocks until it stops. When a TLSConfig is set it
// serves HTTPS (ListenAndServeTLS); otherwise it serves plain HTTP.
func (s *Server) Start() error {
	if s.server.TLSConfig != nil {
		logging.Info(packageName, "Starting HTTPS server on %s", s.server.Addr)
		// Cert and key are supplied via TLSConfig.GetCertificate, so the file
		// arguments are intentionally empty.
		if err := s.server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			logging.Error(packageName, "Server failed: %v", err)
			return fmt.Errorf("server failed: %w", err)
		}
		logging.Info(packageName, "HTTPS server stopped")
		return nil
	}

	logging.Info(packageName, "Starting HTTP server on %s", s.server.Addr)
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logging.Error(packageName, "Server failed: %v", err)
		return fmt.Errorf("server failed: %w", err)
	}
	logging.Info(packageName, "HTTP server stopped")
	return nil
}

// Stop gracefully shuts down the HTTP server using the configured shutdown timeout
func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	logging.Info(packageName, "Shutting down HTTP server")
	if err := s.server.Shutdown(ctx); err != nil {
		logging.Error(packageName, "Failed to shut down server cleanly: %v", err)
		return fmt.Errorf("failed to shut down server: %w", err)
	}
	return nil
}

// Addr returns the address the server is bound to
func (s *Server) Addr() string {
	return s.server.Addr
}

// handleAPITime responds with the current UTC timestamp as JSON.
// Lives at /api/time so the SPA bundle (registered later at "/") can own the root.
func (s *Server) handleAPITime(w http.ResponseWriter, r *http.Request) {
	logging.Debug(packageName, "%s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	now := time.Now().UTC()
	resp := map[string]string{
		"utc":          now.Format(time.RFC3339Nano),
		"unix_seconds": fmt.Sprintf("%d", now.Unix()),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logging.Error(packageName, "Failed to encode JSON response: %v", err)
	}
}
