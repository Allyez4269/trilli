package operators

import (
	"context"
	"errors"
	"sync"
	"time"

	"trilli-cmx/system/auth"
	"trilli-cmx/system/database/postgres"
	"trilli-cmx/system/logging"
)

// Login-flow sentinel errors mapped to HTTP responses by the handlers.
var (
	ErrInvalidCredentials = errors.New("operators: invalid email or password")
	ErrAccountLocked      = errors.New("operators: account is locked — contact a Global admin")
	ErrAccountSuspended   = errors.New("operators: account is suspended")
	ErrGeoBlocked         = errors.New("operators: login not permitted from this location")
	ErrChallengeNotFound  = errors.New("operators: login challenge expired — start over")
)

// Service is the operator identity + auth engine.
type Service struct {
	db      *postgres.Client
	geo     GeoResolver
	mu      sync.Mutex
	pending map[string]*challenge // password-verified, awaiting 2FA
}

// NewService constructs the operator Service. geo may be nil (geo-fencing then
// treats every login as unresolved → fenced operators are blocked, unfenced
// operators pass).
func NewService(db *postgres.Client, geo GeoResolver) *Service {
	return &Service{
		db:      db,
		geo:     geo,
		pending: make(map[string]*challenge),
	}
}

// hashPassword wraps the auth package so the policy floor lives in one place.
func (s *Service) hashPassword(plain string) (string, error) {
	return auth.HashPassword(plain)
}

func logfailLoginEvent(email string, outcome LoginOutcome, err error) {
	logging.Error(packageName, "failed to record login event (email=%s outcome=%s): %v", email, outcome, err)
}

// Locate resolves an IP to a Geo (empty when no resolver / no data).
func (s *Service) Locate(ip string) Geo {
	if s.geo == nil {
		return Geo{}
	}
	return s.geo.Locate(ip)
}

// challenge is a short-lived, password-verified pre-auth state. It exists only
// in memory: a restart simply forces the operator to re-enter their password,
// which is acceptable and avoids persisting a half-authenticated state.
type challenge struct {
	id         string
	operatorID int64
	email      string
	needEnroll bool // operator has no confirmed TOTP → must enroll to finish
	lc         LoginContext
	expiresAt  time.Time
}

// Stage tells the SPA which second factor screen to show.
type Stage string

const (
	StageTOTP   Stage = "totp"   // enrolled — prompt for a code
	StageEnroll Stage = "enroll" // not enrolled — force enrollment (SPEC §6.9)
)

// LoginResult is returned by BeginLogin after a valid password.
type LoginResult struct {
	ChallengeID string `json:"challenge_id"`
	Stage       Stage  `json:"stage"`
}

// BeginLogin validates email/password, enforces lockout + geo-fence, records
// the attempt, and (on success) issues a pre-auth challenge for the mandatory
// second factor. It never creates a session by itself — 2FA must complete.
func (s *Service) BeginLogin(ctx context.Context, email, password string, lc LoginContext) (*LoginResult, error) {
	op, err := s.GetByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		// Unknown email: still hash to blunt timing oracles, record, reject.
		_, _ = s.hashPassword("decoy-password-to-equalize-timing")
		s.recordLoginEvent(ctx, nil, email, OutcomeUnknownEmail, lc)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	// Account state gates.
	switch op.Status {
	case StatusLocked:
		s.recordLoginEvent(ctx, &op.ID, email, OutcomeLockedOut, lc)
		return nil, ErrAccountLocked
	case StatusSuspended:
		s.recordLoginEvent(ctx, &op.ID, email, OutcomeSuspended, lc)
		return nil, ErrAccountSuspended
	}

	// Password. The hash is fetched separately so it never rides on the
	// JSON-serialized Operator.
	hash, herr := s.passwordHash(ctx, op.ID)
	if herr != nil {
		return nil, herr
	}
	ok, verr := auth.VerifyPassword(hash, password)
	if verr != nil || !ok {
		updated, _ := s.recordFailedLogin(ctx, op.ID)
		outcome := OutcomeBadPassword
		if updated != nil && updated.Status == StatusLocked {
			outcome = OutcomeLockedOut
		}
		s.recordLoginEvent(ctx, &op.ID, email, outcome, lc)
		if outcome == OutcomeLockedOut {
			return nil, ErrAccountLocked
		}
		return nil, ErrInvalidCredentials
	}

	// Geo-fence (SPEC §6.9). Evaluated AFTER a correct password so we never
	// leak fence config to anonymous probers.
	rules, err := s.geofenceRules(ctx, op.ID)
	if err != nil {
		return nil, err
	}
	if !geofenceAllows(op.GeofenceEnabled, rules, lc.Geo) {
		s.recordLoginEvent(ctx, &op.ID, email, OutcomeGeoBlocked, lc)
		return nil, ErrGeoBlocked
	}

	// Password good — issue the pre-auth challenge for the mandatory 2nd factor.
	enrolled, err := s.HasConfirmedTOTP(ctx, op.ID)
	if err != nil {
		return nil, err
	}
	chID, err := newSessionToken()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.pending[chID] = &challenge{
		id:         chID,
		operatorID: op.ID,
		email:      op.Email,
		needEnroll: !enrolled,
		lc:         lc,
		expiresAt:  time.Now().Add(ChallengeTTL),
	}
	s.mu.Unlock()

	stage := StageTOTP
	if !enrolled {
		stage = StageEnroll
	}
	return &LoginResult{ChallengeID: chID, Stage: stage}, nil
}

// takeChallenge fetches (without consuming) a live challenge.
func (s *Service) getChallenge(id string) (*challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.pending[id]
	if !ok || time.Now().After(c.expiresAt) {
		if ok {
			delete(s.pending, id)
		}
		return nil, ErrChallengeNotFound
	}
	return c, nil
}

// dropChallenge consumes a challenge so it can't be replayed.
func (s *Service) dropChallenge(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

// ChallengeSetup begins TOTP enrollment for a pending enroll-stage challenge,
// returning the QR payload. Valid only while the challenge is alive.
func (s *Service) ChallengeSetup(ctx context.Context, challengeID string) (*TOTPSetup, error) {
	c, err := s.getChallenge(challengeID)
	if err != nil {
		return nil, err
	}
	return s.BeginTOTPSetup(ctx, c.operatorID, c.email)
}

// CompleteEnroll confirms the enrollment code, activates 2FA, mints recovery
// codes, and finishes login by creating a session. Returns the session and the
// one-time recovery codes (shown once).
func (s *Service) CompleteEnroll(ctx context.Context, challengeID, code string) (*Session, []string, error) {
	c, err := s.getChallenge(challengeID)
	if err != nil {
		return nil, nil, err
	}
	if !c.needEnroll {
		return nil, nil, ErrInvalidCode
	}
	codes, err := s.ConfirmTOTP(ctx, c.operatorID, code)
	if err != nil {
		if errors.Is(err, ErrInvalidCode) {
			s.recordLoginEvent(ctx, &c.operatorID, c.email, OutcomeTwoFAFail, c.lc)
		}
		return nil, nil, err
	}
	sess, err := s.finishLogin(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	return sess, codes, nil
}

// CompleteLogin verifies a TOTP/recovery code for an enrolled operator and
// finishes login by creating a session.
func (s *Service) CompleteLogin(ctx context.Context, challengeID, code string) (*Session, error) {
	c, err := s.getChallenge(challengeID)
	if err != nil {
		return nil, err
	}
	if c.needEnroll {
		// Enrollment-stage challenge can't complete via the plain code path.
		return nil, ErrChallengeNotFound
	}
	if err := s.verifyTOTP(ctx, c.operatorID, code); err != nil {
		if errors.Is(err, ErrInvalidCode) || errors.Is(err, ErrNotEnrolled) {
			s.recordLoginEvent(ctx, &c.operatorID, c.email, OutcomeTwoFAFail, c.lc)
		}
		return nil, err
	}
	return s.finishLogin(ctx, c)
}

// finishLogin clears strikes, records success, mints a session, and consumes
// the challenge. Shared by the enroll and TOTP paths.
func (s *Service) finishLogin(ctx context.Context, c *challenge) (*Session, error) {
	if err := s.clearFailedLogins(ctx, c.operatorID); err != nil {
		return nil, err
	}
	sess, err := s.createSession(ctx, c.operatorID, c.lc.IP, c.lc.Geo, c.lc.UserAgent)
	if err != nil {
		return nil, err
	}
	s.recordLoginEvent(ctx, &c.operatorID, c.email, OutcomeSuccess, c.lc)
	s.dropChallenge(c.id)
	return sess, nil
}

// StepUp re-verifies a fresh 2FA code within an active session and stamps the
// step-up window (SPEC §6.9). Used to gate consequential/destructive actions.
func (s *Service) StepUp(ctx context.Context, sessionID string, operatorID int64, code string) error {
	if err := s.verifyTOTP(ctx, operatorID, code); err != nil {
		return err
	}
	return s.markStepUp(ctx, sessionID)
}

// SweepChallenges drops expired pre-auth challenges. Call periodically.
func (s *Service) SweepChallenges() {
	now := time.Now()
	s.mu.Lock()
	for id, c := range s.pending {
		if now.After(c.expiresAt) {
			delete(s.pending, id)
		}
	}
	s.mu.Unlock()
}
