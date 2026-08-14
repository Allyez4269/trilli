// Package tls provides in-process TLS termination for the Trilli CMX server.
//
// It loads the deployment's certificate keypair and produces a
// *crypto/tls.Config the server serves directly, so CMX can run on its own IP
// without a TLS-terminating reverse proxy. Mirrors the app's system/tls
// package.
//
// The keypair paths come from TRILLI_TLS_CERT / TRILLI_TLS_KEY, defaulting to
// certs/fullchain.pem and certs/privkey.pem under the working directory.
//
// Certificates auto-renew (via the certbot deploy hook), so the Manager reloads
// the keypair from disk whenever the cert or key file changes — picked up on the
// next handshake via tls.Config.GetCertificate, with no restart. On a failed
// reload (e.g. a partial write mid-renewal) the last good certificate keeps
// serving.
package tls

import (
	cryptotls "crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"

	"trilli-cmx/system/logging"
)

const packageName = "tls"

// Configuration defaults. Override the keypair location with TRILLI_TLS_CERT /
// TRILLI_TLS_KEY (e.g. paths kept current by a certbot deploy hook).
const (
	defaultCertFile   = "certs/fullchain.pem"
	defaultKeyFile    = "certs/privkey.pem"
	defaultMinVersion = cryptotls.VersionTLS12
)

// Config holds the TLS configuration.
type Config struct {
	CertFile   string // PEM certificate chain (fullchain.pem)
	KeyFile    string // PEM private key (privkey.pem)
	MinVersion uint16 // minimum accepted TLS version
}

// DefaultConfig returns a Config populated from the environment, falling back
// to the default cert paths.
func DefaultConfig() *Config {
	cert := os.Getenv("TRILLI_TLS_CERT")
	if cert == "" {
		cert = defaultCertFile
	}
	key := os.Getenv("TRILLI_TLS_KEY")
	if key == "" {
		key = defaultKeyFile
	}
	return &Config{
		CertFile:   cert,
		KeyFile:    key,
		MinVersion: defaultMinVersion,
	}
}

// Manager loads and hot-reloads the TLS keypair from disk.
type Manager struct {
	cfg *Config

	mu      sync.RWMutex
	cert    *cryptotls.Certificate
	certMod time.Time
	keyMod  time.Time
}

// New creates a Manager and loads the initial keypair, returning an error if the
// certificate or key cannot be read or parsed (fail fast at startup).
func New(cfg *Config) (*Manager, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	m := &Manager{cfg: cfg}
	if err := m.reload(); err != nil {
		return nil, err
	}
	logging.Info(packageName, "loaded TLS certificate (cert=%s, key=%s)", cfg.CertFile, cfg.KeyFile)
	return m, nil
}

// reload reads the keypair from disk and caches it with the files' mod times.
func (m *Manager) reload() error {
	cert, err := cryptotls.LoadX509KeyPair(m.cfg.CertFile, m.cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("tls: load keypair: %w", err)
	}
	certMod, keyMod := fileModTime(m.cfg.CertFile), fileModTime(m.cfg.KeyFile)
	m.mu.Lock()
	m.cert = &cert
	m.certMod, m.keyMod = certMod, keyMod
	m.mu.Unlock()
	return nil
}

// stale reports whether the cert or key file on disk is newer than what we hold.
func (m *Manager) stale() bool {
	m.mu.RLock()
	cm, km := m.certMod, m.keyMod
	m.mu.RUnlock()
	return fileModTime(m.cfg.CertFile).After(cm) || fileModTime(m.cfg.KeyFile).After(km)
}

// GetCertificate returns the cached certificate, reloading first if the files
// changed on disk (e.g. after a renewal). Wired into the tls.Config so renewals
// are picked up on the next handshake; on reload failure it keeps the previous
// good certificate.
func (m *Manager) GetCertificate(*cryptotls.ClientHelloInfo) (*cryptotls.Certificate, error) {
	if m.stale() {
		if err := m.reload(); err != nil {
			logging.Error(packageName, "certificate reload failed, keeping previous: %v", err)
		} else {
			logging.Info(packageName, "reloaded renewed TLS certificate")
		}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cert, nil
}

// TLSConfig returns a *crypto/tls.Config that serves the managed certificate
// with hot-reload. Pass it to the HTTP server.
func (m *Manager) TLSConfig() *cryptotls.Config {
	return &cryptotls.Config{
		MinVersion:     m.cfg.MinVersion,
		GetCertificate: m.GetCertificate,
	}
}

// fileModTime returns the modification time of path, or the zero time if it
// cannot be stat'd.
func fileModTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
