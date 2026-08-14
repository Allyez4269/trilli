// Package operators implements CMX operator identity, authentication, and the
// hardened login flow (SPEC §3, §6.9): email/password + MANDATORY TOTP 2FA,
// 3-strike lockout, per-operator geo-fencing via qserve, an append-only login
// audit, and short god-mode sessions (15-min idle / 12-hour hard cap).
//
// Operators live in CMX-owned tables (cmx_operators et al.), deliberately
// separate from the app's customer `users` for trust-domain isolation (SPEC §9).
package operators

import "time"

const packageName = "operators"

// Role is the operator authority tier (SPEC §3). Global is a strict superset
// of CMX.
type Role string

const (
	RoleGlobal Role = "global" // super admin — full, unfettered purview
	RoleCMX    Role = "cmx"    // day-to-day tenant operator / support agent
)

// Valid reports whether r is a recognized role.
func (r Role) Valid() bool { return r == RoleGlobal || r == RoleCMX }

// Status is the operator account state.
type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended" // admin-disabled
	StatusLocked    Status = "locked"    // 3-strike failed-login lockout
)

// Security policy constants (SPEC §6.9).
const (
	// MaxFailedLogins is the 3-strike lockout threshold.
	MaxFailedLogins = 3
	// MinActiveGlobals is the minimum number of active Global admins that must
	// remain after any demote/suspend. SPEC §6.9 requires ≥2 so a locked-out or
	// 2FA-lost Global can always be recovered by another Global.
	MinActiveGlobals = 2
	// IdleTimeout is the inactivity window after which a session is dead.
	IdleTimeout = 15 * time.Minute
	// MaxSessionLifetime is the hard cap on a session regardless of activity.
	MaxSessionLifetime = 12 * time.Hour
	// StepUpWindow is how long a fresh 2FA confirmation authorizes consequential
	// actions before another step-up is required.
	StepUpWindow = 5 * time.Minute
	// ChallengeTTL bounds how long a password-verified pre-auth challenge lives
	// before the operator must re-enter their password.
	ChallengeTTL = 5 * time.Minute
)

// Operator is a CMX staff account.
type Operator struct {
	ID               int64      `json:"id"`
	Email            string     `json:"email"`
	Name             string     `json:"name"`
	Role             Role       `json:"role"`
	Status           Status     `json:"status"`
	FailedLoginCount int        `json:"failed_login_count"`
	LockedAt         *time.Time `json:"locked_at,omitempty"`
	GeofenceEnabled  bool       `json:"geofence_enabled"`
	LastLoginAt      *time.Time `json:"last_login_at,omitempty"`
	CreatedBy        *int64     `json:"created_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// IsGlobal reports whether the operator has Global-admin authority.
func (o *Operator) IsGlobal() bool { return o.Role == RoleGlobal }

// Session is a server-side operator session. The ID doubles as the cookie value.
type Session struct {
	ID            string
	OperatorID    int64
	IP            string
	ContinentCode string
	CountryCode   string
	Region        string
	UserAgent     string
	StepUpAt      *time.Time
	CreatedAt     time.Time
	LastSeenAt    time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
}

// Live reports whether the session is currently usable: not revoked, within the
// 12-hour hard cap, and active within the 15-minute idle window.
func (s *Session) Live(now time.Time) bool {
	if s.RevokedAt != nil {
		return false
	}
	if !now.Before(s.ExpiresAt) {
		return false
	}
	if now.Sub(s.LastSeenAt) > IdleTimeout {
		return false
	}
	return true
}

// StepUpFresh reports whether a step-up 2FA confirmation is still within window.
func (s *Session) StepUpFresh(now time.Time) bool {
	return s.StepUpAt != nil && now.Sub(*s.StepUpAt) <= StepUpWindow
}

// Geo is the resolved geolocation of a login IP (best-effort; zero when the IP
// can't be located).
type Geo struct {
	ContinentCode string
	CountryCode   string
	Region        string
}

// Resolved reports whether any meaningful region was determined.
func (g Geo) Resolved() bool {
	return g.ContinentCode != "" || g.CountryCode != ""
}

// LoginOutcome is the recorded result of a login attempt (cmx_login_events).
type LoginOutcome string

const (
	OutcomeSuccess      LoginOutcome = "success"
	OutcomeBadPassword  LoginOutcome = "bad_password"
	OutcomeLockedOut    LoginOutcome = "locked_out"
	OutcomeTwoFAFail    LoginOutcome = "twofa_fail"
	OutcomeGeoBlocked   LoginOutcome = "geo_blocked"
	OutcomeSuspended    LoginOutcome = "suspended"
	OutcomeUnknownEmail LoginOutcome = "unknown_email"
)
