// Package passkeys implements WebAuthn passkeys: passwordless, phishing-resistant
// sign-in backed by public-key credentials. The server stores only public keys;
// the private key never leaves the user's device.
//
// Two ceremonies, each a begin → finish handshake:
//   - Registration (logged-in user enrolls a passkey from Settings)
//   - Login        (a passkey alone signs the user in — discoverable/usernameless)
//
// The per-ceremony challenge (go-webauthn SessionData) is round-tripped to the
// browser as an ENCRYPTED, short-lived "state" token (AES-GCM via the app key),
// so no extra server-side storage is needed and the client can't tamper with it.
package passkeys

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgconn"

	appcrypto "trilli/system/crypto"
	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const (
	packageName = "passkeys"
	stateTTL    = 5 * time.Minute
)

// Sentinel errors mapped to HTTP statuses by the handlers.
var (
	ErrInvalidState     = errors.New("passkeys: your request expired — start again")
	ErrCredentialUsed   = errors.New("passkeys: this passkey is already registered")
	ErrInvalidAssertion = errors.New("passkeys: that passkey could not be verified")
	ErrNotFound         = errors.New("passkeys: passkey not found")
)

// Service wires the DB client and a configured WebAuthn relying party.
type Service struct {
	client *postgres.Client
	web    *webauthn.WebAuthn
}

// NewService builds the WebAuthn relying party from env (with prod defaults).
// PASSKEY_RP_ID is permanent — every passkey is bound to it. PASSKEY_RP_ORIGINS
// is a comma-separated allowlist of the exact origins the SPA is served from.
func NewService(c *postgres.Client) (*Service, error) {
	rpID := envOr("PASSKEY_RP_ID", "trilli.com")
	rpName := envOr("PASSKEY_RP_NAME", "Trilli")
	origins := splitCSV(envOr("PASSKEY_RP_ORIGINS", "https://app.trilli.com"))
	w, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: rpName,
		RPOrigins:     origins,
	})
	if err != nil {
		return nil, fmt.Errorf("passkeys: webauthn init: %w", err)
	}
	logging.Info(packageName, "WebAuthn relying party id=%q origins=%v", rpID, origins)
	return &Service{client: c, web: w}, nil
}

// ----- webauthn.User adapter ------------------------------------------------

// webUser adapts a Trilli user to the go-webauthn User interface. The user
// handle is the 8-byte big-endian user id, so a discoverable login can map the
// userHandle the authenticator returns straight back to our user.
type webUser struct {
	id          int64
	name        string
	displayName string
	creds       []webauthn.Credential
}

func (u *webUser) WebAuthnID() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(u.id))
	return b
}
func (u *webUser) WebAuthnName() string                       { return u.name }
func (u *webUser) WebAuthnDisplayName() string                { return u.displayName }
func (u *webUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func userIDFromHandle(h []byte) (int64, error) {
	if len(h) != 8 {
		return 0, fmt.Errorf("passkeys: bad user handle (%d bytes)", len(h))
	}
	return int64(binary.BigEndian.Uint64(h)), nil
}

// loadWebUser builds the webauthn.User for a user id, including all their stored
// credentials (needed for exclusion lists and assertion validation).
func (s *Service) loadWebUser(ctx context.Context, userID int64) (*webUser, error) {
	var email string
	var fullName sql.NullString
	err := s.client.QueryRowContext(ctx,
		`SELECT email, full_name FROM users WHERE id = $1`, userID,
	).Scan(&email, &fullName)
	if err != nil {
		return nil, err
	}
	creds, err := s.loadCredentials(ctx, userID)
	if err != nil {
		return nil, err
	}
	display := email
	if fullName.Valid && strings.TrimSpace(fullName.String) != "" {
		display = strings.TrimSpace(fullName.String)
	}
	return &webUser{id: userID, name: email, displayName: display, creds: creds}, nil
}

func (s *Service) loadCredentials(ctx context.Context, userID int64) ([]webauthn.Credential, error) {
	rows, err := s.client.QueryContext(ctx,
		`SELECT credential FROM user_passkeys WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("passkeys: load credentials: %w", err)
	}
	defer rows.Close()
	out := make([]webauthn.Credential, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("passkeys: scan credential: %w", err)
		}
		var c webauthn.Credential
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("passkeys: decode credential: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ----- challenge state token ------------------------------------------------

type stateEnvelope struct {
	UserID  int64                `json:"uid"` // 0 for a discoverable login (no user yet)
	Expires int64                `json:"exp"`
	Session webauthn.SessionData `json:"sd"`
}

func (s *Service) sealState(userID int64, sd *webauthn.SessionData) (string, error) {
	raw, err := json.Marshal(stateEnvelope{
		UserID:  userID,
		Expires: time.Now().Add(stateTTL).Unix(),
		Session: *sd,
	})
	if err != nil {
		return "", fmt.Errorf("passkeys: encode state: %w", err)
	}
	enc, err := appcrypto.Encrypt(raw)
	if err != nil {
		return "", fmt.Errorf("passkeys: seal state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(enc), nil
}

func (s *Service) openState(token string) (*stateEnvelope, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return nil, ErrInvalidState
	}
	dec, err := appcrypto.Decrypt(raw)
	if err != nil {
		return nil, ErrInvalidState
	}
	var env stateEnvelope
	if err := json.Unmarshal(dec, &env); err != nil {
		return nil, ErrInvalidState
	}
	if time.Now().Unix() > env.Expires {
		return nil, ErrInvalidState
	}
	return &env, nil
}

// ----- registration ---------------------------------------------------------

// BeginRegistration starts enrolling a new passkey for a logged-in user. It
// returns the creation options for navigator.credentials.create() plus the
// sealed challenge state the client echoes back to FinishRegistration.
func (s *Service) BeginRegistration(ctx context.Context, userID int64) (*protocol.CredentialCreation, string, error) {
	u, err := s.loadWebUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	// Exclude already-registered credentials so the authenticator won't make a
	// duplicate on the same device.
	excl := make([]protocol.CredentialDescriptor, 0, len(u.creds))
	for _, c := range u.creds {
		excl = append(excl, protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: c.ID,
			Transport:    c.Transport,
		})
	}
	creation, sd, err := s.web.BeginRegistration(u,
		webauthn.WithExclusions(excl),
		// Discoverable (resident) key so the user can sign in without typing an
		// email; REQUIRE user verification (biometric/PIN) so the passkey is a
		// true two-factor — otherwise a passwordless login could skip the gesture.
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return nil, "", fmt.Errorf("passkeys: begin registration: %w", err)
	}
	token, err := s.sealState(userID, sd)
	if err != nil {
		return nil, "", err
	}
	return creation, token, nil
}

// FinishRegistration verifies the attestation and stores the new passkey.
// The request body must be the raw credential JSON (the state arrives via the
// handler, not the body). label is a user-facing device name.
func (s *Service) FinishRegistration(ctx context.Context, userID int64, stateToken, label string, r *http.Request) error {
	env, err := s.openState(stateToken)
	if err != nil {
		return err
	}
	if env.UserID != userID {
		return ErrInvalidState
	}
	u, err := s.loadWebUser(ctx, userID)
	if err != nil {
		return err
	}
	cred, err := s.web.FinishRegistration(u, env.Session, r)
	if err != nil {
		logging.Error(packageName, "FinishRegistration verify failed: %v", err)
		return ErrInvalidAssertion
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("passkeys: encode new credential: %w", err)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Passkey"
	}
	if len(label) > 60 {
		label = label[:60]
	}
	_, err = s.client.ExecContext(ctx,
		`INSERT INTO user_passkeys (user_id, credential_id, credential, label) VALUES ($1, $2, $3, $4)`,
		userID, cred.ID, raw, label,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrCredentialUsed
		}
		return fmt.Errorf("passkeys: store credential: %w", err)
	}
	logging.Info(packageName, "FinishRegistration: user=%d enrolled a passkey", userID)
	return nil
}

// ----- login (discoverable / usernameless) ----------------------------------

// BeginLogin starts a passwordless passkey sign-in. No user is known yet — the
// authenticator picks a discoverable credential. Returns request options for
// navigator.credentials.get() plus the sealed challenge state.
func (s *Service) BeginLogin() (*protocol.CredentialAssertion, string, error) {
	// Require user verification so the sign-in always demands the biometric/PIN
	// gesture — the passkey can't be used silently.
	assertion, sd, err := s.web.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return nil, "", fmt.Errorf("passkeys: begin login: %w", err)
	}
	token, err := s.sealState(0, sd)
	if err != nil {
		return nil, "", err
	}
	return assertion, token, nil
}

// FinishLogin verifies an assertion and returns the authenticated user id. The
// request body must be the raw assertion JSON. On success the signature counter
// + last-used time are persisted.
func (s *Service) FinishLogin(ctx context.Context, stateToken string, r *http.Request) (int64, error) {
	env, err := s.openState(stateToken)
	if err != nil {
		return 0, err
	}
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		uid, err := userIDFromHandle(userHandle)
		if err != nil {
			return nil, err
		}
		return s.loadWebUser(ctx, uid)
	}
	user, cred, err := s.web.FinishPasskeyLogin(handler, env.Session, r)
	if err != nil {
		logging.Error(packageName, "FinishLogin verify failed: %v", err)
		return 0, ErrInvalidAssertion
	}
	wu, ok := user.(*webUser)
	if !ok {
		return 0, ErrInvalidAssertion
	}
	// Persist the bumped signature counter (clone detection) + last-used time.
	if raw, mErr := json.Marshal(cred); mErr == nil {
		if _, uErr := s.client.ExecContext(ctx,
			`UPDATE user_passkeys SET credential = $1, last_used_at = NOW() WHERE credential_id = $2`,
			raw, cred.ID,
		); uErr != nil {
			logging.Error(packageName, "FinishLogin: update counter failed: %v", uErr)
		}
	}
	logging.Info(packageName, "FinishLogin: user=%d signed in with a passkey", wu.id)
	return wu.id, nil
}

// ----- management -----------------------------------------------------------

// PasskeyInfo is one registered passkey, for the Settings management list.
type PasskeyInfo struct {
	ID         int64      `json:"id"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// List returns the user's registered passkeys, newest first.
func (s *Service) List(ctx context.Context, userID int64) ([]PasskeyInfo, error) {
	rows, err := s.client.QueryContext(ctx,
		`SELECT id, label, created_at, last_used_at FROM user_passkeys WHERE user_id = $1 ORDER BY created_at DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("passkeys: list: %w", err)
	}
	defer rows.Close()
	out := make([]PasskeyInfo, 0)
	for rows.Next() {
		var p PasskeyInfo
		var last sql.NullTime
		if err := rows.Scan(&p.ID, &p.Label, &p.CreatedAt, &last); err != nil {
			return nil, fmt.Errorf("passkeys: scan: %w", err)
		}
		if last.Valid {
			p.LastUsedAt = &last.Time
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Rename sets a passkey's label, scoped to the owning user.
func (s *Service) Rename(ctx context.Context, userID, id int64, label string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Passkey"
	}
	if len(label) > 60 {
		label = label[:60]
	}
	res, err := s.client.ExecContext(ctx,
		`UPDATE user_passkeys SET label = $1 WHERE id = $2 AND user_id = $3`, label, id, userID)
	if err != nil {
		return fmt.Errorf("passkeys: rename: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a passkey, scoped to the owning user.
func (s *Service) Delete(ctx context.Context, userID, id int64) error {
	res, err := s.client.ExecContext(ctx,
		`DELETE FROM user_passkeys WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("passkeys: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	logging.Info(packageName, "Delete: user=%d removed passkey=%d", userID, id)
	return nil
}

// ----- step-up (re-auth with an existing passkey) ---------------------------

// Count returns how many passkeys a user has registered.
func (s *Service) Count(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := s.client.QueryRowContext(ctx,
		`SELECT count(*) FROM user_passkeys WHERE user_id = $1`, userID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("passkeys: count: %w", err)
	}
	return n, nil
}

// BeginStepUp starts a re-authentication for an already-logged-in user (used as
// a step-up before a sensitive action). Unlike login it's scoped to this user's
// own credentials and always requires user verification.
func (s *Service) BeginStepUp(ctx context.Context, userID int64) (*protocol.CredentialAssertion, string, error) {
	u, err := s.loadWebUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if len(u.creds) == 0 {
		return nil, "", ErrNotFound
	}
	assertion, sd, err := s.web.BeginLogin(u, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, "", fmt.Errorf("passkeys: begin step-up: %w", err)
	}
	token, err := s.sealState(userID, sd)
	if err != nil {
		return nil, "", err
	}
	return assertion, token, nil
}

// VerifyStepUp validates a step-up assertion against the given user's
// credentials. Request body is the raw assertion JSON. Returns nil when the
// passkey checks out (proving it's really them).
func (s *Service) VerifyStepUp(ctx context.Context, userID int64, stateToken string, r *http.Request) error {
	env, err := s.openState(stateToken)
	if err != nil {
		return err
	}
	if env.UserID != userID {
		return ErrInvalidState
	}
	u, err := s.loadWebUser(ctx, userID)
	if err != nil {
		return err
	}
	cred, err := s.web.FinishLogin(u, env.Session, r)
	if err != nil {
		logging.Error(packageName, "VerifyStepUp failed: %v", err)
		return ErrInvalidAssertion
	}
	if raw, mErr := json.Marshal(cred); mErr == nil {
		if _, uErr := s.client.ExecContext(ctx,
			`UPDATE user_passkeys SET credential = $1, last_used_at = NOW() WHERE credential_id = $2`,
			raw, cred.ID,
		); uErr != nil {
			logging.Error(packageName, "VerifyStepUp: update counter failed: %v", uErr)
		}
	}
	return nil
}

// ----- helpers --------------------------------------------------------------

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
