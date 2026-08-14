package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"trilli/system/database/postgres"
)

const packageName = "session"

// CookieName is the HTTP cookie key the SPA sends as the session token.
// Exported so handlers in other packages (auth, invites, ...) all use
// the same name — they MUST stay in sync or the middleware won't find
// the session.
const CookieName = "trilli_session"

// SetCookie writes the session cookie on a response. Always marked Secure:
// TLS terminates upstream (Cloudflare/nginx) so r.TLS is nil here even though
// every real request is HTTPS — keying off r.TLS would ship the session token
// without the Secure attribute. Browsers treat localhost/127.0.0.1 as
// trustworthy origins, so Secure cookies still work in local dev.
func SetCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie removes the session cookie (used on logout).
func ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// Scope distinguishes tenant-side sessions from super-admin sessions.
type Scope string

const (
	ScopeTenant Scope = "tenant"
	ScopeSuper  Scope = "super"
)

// DefaultTTL is the default session lifetime applied when CreateInput.TTL is zero.
const DefaultTTL = 30 * 24 * time.Hour

// DefaultIdleTimeout is the inactivity window: a session with no authenticated
// request for this long is treated as logged out (rejected + revoked on the
// next request). This is the single source of truth for the timeout — the
// value is surfaced to the frontend via the identity payload so the client can
// auto-log-out on the same window, and it's the place to wire a per-tenant /
// admin-CMS override in the future. 30 minutes of inactivity.
const DefaultIdleTimeout = 30 * time.Minute

// GeoInfo is the resolved geolocation for a session's IP (best-effort; all
// fields are empty/zero when the IP can't be located).
type GeoInfo struct {
	CountryCode string
	Country     string
	Region      string
	City        string
	PostalCode  string
	Latitude    float64
	Longitude   float64
}

// HasLocation reports whether any meaningful geo was resolved.
func (g GeoInfo) HasLocation() bool {
	return g.CountryCode != "" || g.City != "" || g.Latitude != 0 || g.Longitude != 0
}

// GeoResolver turns an IP into a GeoInfo. Implemented by an adapter over the
// qserve GeoIP service and injected into the store, so the session package
// stays decoupled from qserve.
type GeoResolver interface {
	Locate(ip string) GeoInfo
}

// Session is the server-side session record. The ID doubles as the cookie value.
type Session struct {
	ID         string
	UserID     int64
	TenantID   *int64
	Scope      Scope
	IP         string // authoritative connecting IP (v4 or v6)
	IPv4       string // client's IPv4 (from the post-login probe; may be empty)
	IPv6       string // client's IPv6 (from the post-login probe; may be empty)
	UserAgent  string
	Geo        GeoInfo
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

// Active reports whether the session is currently valid.
func (s *Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// CreateInput captures the data needed to create a new session.
type CreateInput struct {
	UserID    int64
	TenantID  *int64
	Scope     Scope
	IP        string
	UserAgent string
	TTL       time.Duration
}

// Store is the persistence interface for sessions.
type Store interface {
	Create(ctx context.Context, in CreateInput) (*Session, error)
	Get(ctx context.Context, id string) (*Session, error)
	Touch(ctx context.Context, id string, extendBy time.Duration) error
	// UpdateTenant re-binds a live tenant-scope session to a different tenant
	// (used by the account switcher). Callers MUST first verify the user has an
	// active membership in the target tenant.
	UpdateTenant(ctx context.Context, id string, tenantID int64) error
	Revoke(ctx context.Context, id string) error
	RevokeAllForUser(ctx context.Context, userID int64) error
	PurgeExpired(ctx context.Context, olderThan time.Duration) (int64, error)
	// SetGeoResolver wires an IP→geolocation resolver used at Create time. When
	// unset, sessions are created without geo (graceful).
	SetGeoResolver(GeoResolver)
	// UpdateIPs records the client's probed IPv4/IPv6 on a session and re-resolves
	// geolocation, preferring IPv4 when present (else IPv6).
	UpdateIPs(ctx context.Context, sessionID, ipv4, ipv6 string) error
}

// Sentinel errors.
var (
	ErrNotFound = errors.New("session: not found")
	ErrInvalid  = errors.New("session: invalid")
)

type store struct {
	client *postgres.Client
	geo    GeoResolver
}

// NewStore returns a Postgres-backed session store.
func NewStore(c *postgres.Client) Store {
	return &store{client: c}
}

// SetGeoResolver wires the IP→geolocation resolver used at Create time.
func (s *store) SetGeoResolver(g GeoResolver) {
	s.geo = g
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (s *store) Create(ctx context.Context, in CreateInput) (*Session, error) {
	if in.TTL <= 0 {
		in.TTL = DefaultTTL
	}
	if in.Scope != ScopeTenant && in.Scope != ScopeSuper {
		return nil, fmt.Errorf("%w: bad scope %q", ErrInvalid, in.Scope)
	}
	if in.Scope == ScopeTenant && in.TenantID == nil {
		return nil, fmt.Errorf("%w: tenant scope requires tenant_id", ErrInvalid)
	}
	if in.Scope == ScopeSuper && in.TenantID != nil {
		return nil, fmt.Errorf("%w: super scope must have nil tenant_id", ErrInvalid)
	}

	id, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("session: generate token: %w", err)
	}

	now := time.Now().UTC()
	expires := now.Add(in.TTL)

	var ipArg any
	if in.IP != "" {
		ipArg = in.IP
	}

	// Best-effort geolocation from the client IP (no-op when no resolver is
	// wired, the IP is empty/private, or the lookup misses).
	var geo GeoInfo
	if s.geo != nil && in.IP != "" {
		geo = s.geo.Locate(in.IP)
	}
	var latArg, lonArg any
	if geo.HasLocation() {
		latArg = geo.Latitude
		lonArg = geo.Longitude
	}

	// Seed the per-protocol column matching the connecting IP; the post-login
	// probe fills in the other one via UpdateIPs.
	v4, v6 := splitIPByFamily(in.IP)

	const q = `
		INSERT INTO sessions
			(id, user_id, tenant_id, scope, ip, user_agent,
			 country_code, country, region, city, postal_code, latitude, longitude,
			 ip_v4, ip_v6, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $16, $17)`
	if _, err := s.client.ExecContext(ctx, q,
		id, in.UserID, in.TenantID, string(in.Scope), ipArg, in.UserAgent,
		nullStr(geo.CountryCode), nullStr(geo.Country), nullStr(geo.Region),
		nullStr(geo.City), nullStr(geo.PostalCode), latArg, lonArg,
		nullStr(v4), nullStr(v6), now, expires,
	); err != nil {
		return nil, fmt.Errorf("session: insert: %w", err)
	}

	return &Session{
		ID: id, UserID: in.UserID, TenantID: in.TenantID, Scope: in.Scope,
		IP: in.IP, IPv4: v4, IPv6: v6, UserAgent: in.UserAgent, Geo: geo,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: expires,
	}, nil
}

// UpdateIPs records the client's probed addresses on a session and re-resolves
// geolocation, preferring IPv4 (the more accurate geo source) when present.
// Invalid/empty addresses are ignored; existing values are preserved.
func (s *store) UpdateIPs(ctx context.Context, sessionID, ipv4, ipv6 string) error {
	v4 := validIPOfFamily(ipv4, true)
	v6 := validIPOfFamily(ipv6, false)
	if v4 == "" && v6 == "" {
		return nil
	}

	// Always record whichever addresses we got (COALESCE keeps any prior value).
	const ipQ = `UPDATE sessions SET ip_v4 = COALESCE($2, ip_v4), ip_v6 = COALESCE($3, ip_v6) WHERE id = $1`
	if _, err := s.client.ExecContext(ctx, ipQ, sessionID, nullStr(v4), nullStr(v6)); err != nil {
		return fmt.Errorf("session: update ips: %w", err)
	}

	// Re-resolve geo from IPv4 if present, else IPv6 — only overwrite when we
	// actually get a location (don't wipe good create-time geo on a miss).
	geoSource := v4
	if geoSource == "" {
		geoSource = v6
	}
	if s.geo == nil || geoSource == "" {
		return nil
	}
	geo := s.geo.Locate(geoSource)
	if !geo.HasLocation() {
		return nil
	}
	const geoQ = `
		UPDATE sessions SET
			country_code = $2, country = $3, region = $4, city = $5, postal_code = $6,
			latitude = $7, longitude = $8
		WHERE id = $1`
	if _, err := s.client.ExecContext(ctx, geoQ, sessionID,
		nullStr(geo.CountryCode), nullStr(geo.Country), nullStr(geo.Region),
		nullStr(geo.City), nullStr(geo.PostalCode), geo.Latitude, geo.Longitude,
	); err != nil {
		return fmt.Errorf("session: update geo: %w", err)
	}
	return nil
}

// splitIPByFamily returns (ipv4, "") or ("", ipv6) for a parsed address.
func splitIPByFamily(ip string) (v4, v6 string) {
	p := net.ParseIP(ip)
	if p == nil {
		return "", ""
	}
	if p.To4() != nil {
		return ip, ""
	}
	return "", ip
}

// validIPOfFamily returns the address only if it parses and matches the wanted
// family (wantV4 true → IPv4, false → IPv6); otherwise "".
func validIPOfFamily(ip string, wantV4 bool) string {
	p := net.ParseIP(ip)
	if p == nil {
		return ""
	}
	isV4 := p.To4() != nil
	if isV4 == wantV4 {
		return ip
	}
	return ""
}

// nullStr maps an empty string to a SQL NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *store) Get(ctx context.Context, id string) (*Session, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	const q = `
		SELECT id, user_id, tenant_id, scope,
		       COALESCE(host(ip), ''), COALESCE(host(ip_v4), ''), COALESCE(host(ip_v6), ''),
		       COALESCE(user_agent, ''),
		       COALESCE(country_code, ''), COALESCE(country, ''), COALESCE(region, ''),
		       COALESCE(city, ''), COALESCE(postal_code, ''), latitude, longitude,
		       created_at, last_seen_at, expires_at, revoked_at
		  FROM sessions
		 WHERE id = $1`

	var sess Session
	var scope string
	var revoked sql.NullTime
	var lat, lon sql.NullFloat64

	err := s.client.QueryRowContext(ctx, q, id).Scan(
		&sess.ID, &sess.UserID, &sess.TenantID, &scope,
		&sess.IP, &sess.IPv4, &sess.IPv6, &sess.UserAgent,
		&sess.Geo.CountryCode, &sess.Geo.Country, &sess.Geo.Region,
		&sess.Geo.City, &sess.Geo.PostalCode, &lat, &lon,
		&sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &revoked,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("session: query: %w", err)
	}
	sess.Scope = Scope(scope)
	if lat.Valid {
		sess.Geo.Latitude = lat.Float64
	}
	if lon.Valid {
		sess.Geo.Longitude = lon.Float64
	}
	if revoked.Valid {
		t := revoked.Time
		sess.RevokedAt = &t
	}
	return &sess, nil
}

func (s *store) Touch(ctx context.Context, id string, extendBy time.Duration) error {
	if extendBy <= 0 {
		extendBy = DefaultTTL
	}
	now := time.Now().UTC()
	newExpiry := now.Add(extendBy)
	const q = `
		UPDATE sessions
		   SET last_seen_at = $2,
		       expires_at   = GREATEST(expires_at, $3)
		 WHERE id = $1 AND revoked_at IS NULL AND expires_at > NOW()`
	res, err := s.client.ExecContext(ctx, q, id, now, newExpiry)
	if err != nil {
		return fmt.Errorf("session: touch: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *store) UpdateTenant(ctx context.Context, id string, tenantID int64) error {
	const q = `
		UPDATE sessions
		   SET tenant_id = $2, last_seen_at = NOW()
		 WHERE id = $1 AND scope = 'tenant' AND revoked_at IS NULL AND expires_at > NOW()`
	res, err := s.client.ExecContext(ctx, q, id, tenantID)
	if err != nil {
		return fmt.Errorf("session: update tenant: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *store) Revoke(ctx context.Context, id string) error {
	const q = `UPDATE sessions SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`
	if _, err := s.client.ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("session: revoke: %w", err)
	}
	return nil
}

func (s *store) RevokeAllForUser(ctx context.Context, userID int64) error {
	const q = `UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`
	if _, err := s.client.ExecContext(ctx, q, userID); err != nil {
		return fmt.Errorf("session: revoke all: %w", err)
	}
	return nil
}

func (s *store) PurgeExpired(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan < 0 {
		olderThan = 0
	}
	const q = `DELETE FROM sessions WHERE expires_at < NOW() - $1::interval`
	res, err := s.client.ExecContext(ctx, q, fmt.Sprintf("%d seconds", int64(olderThan.Seconds())))
	if err != nil {
		return 0, fmt.Errorf("session: purge: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
