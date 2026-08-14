// Package microsoftauth implements "Continue with Microsoft" — the OAuth 2.0
// authorization-code flow against the Microsoft identity platform (Entra ID /
// personal Microsoft accounts) — for both sign-in and sign-up.
//
// It mirrors system/googleauth almost exactly; only the provider-specific bits
// differ:
//
//   - endpoints under login.microsoftonline.com/common (multi-tenant + MSA)
//   - the id_token has no `email_verified` claim, so we take the verified
//     account address from `email` (falling back to `preferred_username`); for
//     standard MSA and Entra accounts that address is the one the user proved
//     control of to Microsoft — see the Callback comment for the trade-off.
//
// Flow:
//
//	GET /api/auth/microsoft/start     → redirect to Microsoft's consent screen
//	GET /api/auth/microsoft/callback  → exchange the code for tokens directly
//	                                    with Microsoft (server-to-server, TLS),
//	                                    read the identity from the id_token, then:
//	                                      • existing account → session → /home
//	                                      • new email        → verified OAuth
//	                                        signup intent → /signup/checkout
//
// The id_token is trusted without re-verifying its signature because it is
// fetched over TLS directly from Microsoft's token endpoint using our client
// secret, never via the browser (same rationale as googleauth).
package microsoftauth

import (
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trilli/system/auth"
	"trilli/system/credentials"
	"trilli/system/crypto"
	"trilli/system/logging"
	"trilli/system/session"
	"trilli/system/signupintents"
)

const packageName = "microsoftauth"

const (
	// "common" admits both work/school (Entra ID) and personal Microsoft
	// accounts. The app registration's "supported account types" is the real
	// gate; this just routes through the multi-tenant endpoint.
	microsoftAuthURL  = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
	microsoftTokenURL = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
	stateCookie       = "ms_oauth_state"
	flashCookie       = "trilli_flash"
	// noAccountCookie flags the full-page (popup-blocked) fallback that an
	// authenticated user has no Trilli account yet, so /login shows the friendly
	// "let's get you started" prompt instead of an error. Value = provider name.
	noAccountCookie   = "trilli_oauth_no_account"
	defaultRedirect   = "https://app.trilli.com/api/auth/microsoft/callback"
)

// flash drops a one-time, human-readable message in a short-lived cookie and
// redirects to a CLEAN path (no literal ?error= codes in the URL).
func flash(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.SetCookie(w, &http.Cookie{
		Name: flashCookie, Value: url.QueryEscape(msg), Path: "/",
		MaxAge: 60, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// Service wires the OAuth handlers to auth (login), signup intents (new
// accounts), and the credentials vault (client id/secret).
type Service struct {
	auth     *auth.Service
	intents  *signupintents.Service
	creds    *credentials.Service
	redirect string
}

// NewService builds the handler service. redirectURL must match exactly what's
// registered in the Azure app registration (empty → the app.trilli.com default).
func NewService(a *auth.Service, intents *signupintents.Service, creds *credentials.Service, redirectURL string) *Service {
	if strings.TrimSpace(redirectURL) == "" {
		redirectURL = defaultRedirect
	}
	return &Service{auth: a, intents: intents, creds: creds, redirect: redirectURL}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the public OAuth routes (no RequireAuth — they predate a session).
func (s *Service) Register(m muxLike) {
	m.Handle("GET /api/auth/microsoft/start", http.HandlerFunc(s.Start))
	m.Handle("GET /api/auth/microsoft/callback", http.HandlerFunc(s.Callback))
}

// clientCreds loads the OAuth client id + secret from the vault.
func (s *Service) clientCreds(ctx context.Context) (id, secret string, err error) {
	id, _, err = s.creds.GetActive(ctx, "microsoft", "client_id")
	if err != nil {
		return "", "", fmt.Errorf("microsoftauth: client id: %w", err)
	}
	secret, _, err = s.creds.GetActive(ctx, "microsoft", "client_secret")
	if err != nil {
		return "", "", fmt.Errorf("microsoftauth: client secret: %w", err)
	}
	return id, secret, nil
}

// oauthState is the CSRF-protected, integrity-checked payload carried through
// the round-trip in the `state` parameter (encrypted), with its nonce also set
// as a cookie so a forged callback can't replay someone else's state.
type oauthState struct {
	Nonce  string `json:"n"`
	Plan   string `json:"p"`
	Cycle  string `json:"c"`
	Mode   string `json:"m"` // "signup" | "login"
	Popup  bool   `json:"pu"`
	Origin string `json:"o"` // opener origin to postMessage back to (popup mode)
}

// allowedOrigins are the sites permitted to open the popup and receive its
// postMessage result (the app and the marketing site live on different origins).
var allowedOrigins = map[string]bool{
	"https://app.trilli.com": true,
	"https://www.trilli.com": true,
	"https://trilli.com":     true,
}

func allowedOrigin(o string) string {
	o = strings.TrimSpace(o)
	if allowedOrigins[o] {
		return o
	}
	return ""
}

// succeed completes the flow: in popup mode it posts the next URL back to the
// opener window and closes; otherwise it's a normal top-level redirect.
func (s *Service) succeed(w http.ResponseWriter, r *http.Request, st oauthState, path string) {
	if st.Popup && st.Origin != "" {
		popupRespond(w, st.Origin, map[string]any{"source": "trilli-microsoft", "status": "ok", "redirect": path})
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// fail ends the flow with a message: popup posts the error to the opener (which
// shows it in-place); a full-page flow falls back to a clean flash redirect.
func (s *Service) fail(w http.ResponseWriter, r *http.Request, st oauthState, fallbackPath, msg string) {
	if st.Popup && st.Origin != "" {
		popupRespond(w, st.Origin, map[string]any{"source": "trilli-microsoft", "status": "error", "error": msg})
		return
	}
	flash(w, r, fallbackPath, msg)
}

// handleReauth verifies a step-up OAuth re-authentication (used to confirm a
// sensitive action like account deletion). It does NOT create a session: the
// popup must carry an active session, and the Microsoft email must match that
// signed-in user. On success it hands a short-lived step-up token back through
// the popup channel.
func (s *Service) handleReauth(w http.ResponseWriter, r *http.Request, st oauthState, email string) {
	oauthEmail := strings.ToLower(strings.TrimSpace(email))
	if !st.Popup {
		s.fail(w, r, st, "/settings?tab=account", "Re-authentication must run in a popup.")
		return
	}
	c, cErr := r.Cookie(auth.CookieName)
	if cErr != nil {
		s.fail(w, r, st, "/login", "Your session expired. Please sign in again.")
		return
	}
	identity, iErr := s.auth.LoadIdentity(r.Context(), c.Value)
	if iErr != nil || identity == nil || identity.User == nil {
		s.fail(w, r, st, "/login", "Your session expired. Please sign in again.")
		return
	}
	if strings.ToLower(strings.TrimSpace(identity.User.Email)) != oauthEmail {
		s.fail(w, r, st, "/settings?tab=account", "That Microsoft account doesn't match your Trilli login.")
		return
	}
	token, tErr := s.auth.NewStepUpToken(identity.User.ID)
	if tErr != nil {
		logging.Error(packageName, "handleReauth: step-up token: %v", tErr)
		s.fail(w, r, st, "/settings?tab=account", "Couldn't verify. Please try again.")
		return
	}
	popupRespond(w, st.Origin, map[string]any{"source": "trilli-microsoft", "status": "reauth_ok", "token": token})
}

// noAccount handles an OAuth sign-in whose email has no Trilli account. Rather
// than hard-redirecting, it hands control back to the app login so it can show a
// warm "let's get you started" prompt with a call-to-action to the plans page.
// In popup mode that's a result message the opener acts on; on the full-page
// fallback we set a short-lived flag cookie and bounce to /login, which reads it
// and shows the same prompt.
func (s *Service) noAccount(w http.ResponseWriter, r *http.Request, st oauthState) {
	if st.Popup && st.Origin != "" {
		popupRespond(w, st.Origin, map[string]any{"source": "trilli-microsoft", "status": "no_account"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: noAccountCookie, Value: "microsoft", Path: "/",
		MaxAge: 120, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// set2FAPending hands the pending-2FA token to the login page via a short-lived
// first-party cookie (set inside the popup, on the app origin). SameSite=Lax so
// it survives the top-level navigation to /login.
func set2FAPending(w http.ResponseWriter, token, method string) {
	payload, _ := json.Marshal(map[string]string{"token": token, "method": method})
	http.SetCookie(w, &http.Cookie{
		Name: "trilli_2fa_pending", Value: url.QueryEscape(string(payload)), Path: "/",
		MaxAge: 300, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// popupRespond renders a tiny page that hands the result back to the opener and
// closes the popup. It delivers via TWO channels: (1) localStorage on the app
// origin — the reliable path, since this callback page is same-origin with the
// app and a Cross-Origin-Opener-Policy on the provider's pages can sever
// window.opener; and (2) postMessage, for when the opener link survives (e.g.
// the cross-origin marketing modal). Go's json escapes <,>,& so the payload is
// safe to embed in <script>.
func popupRespond(w http.ResponseWriter, origin string, payload map[string]any) {
	data, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><body><script>
(function(){var p=%s;try{localStorage.setItem('trilli_oauth_result',JSON.stringify(p));}catch(e){}try{if(window.opener)window.opener.postMessage(p,%q);}catch(e){}window.close();})();
</script>You can close this window.</body>`, data, origin)
}

// Start redirects to Microsoft's consent screen.
func (s *Service) Start(w http.ResponseWriter, r *http.Request) {
	// Popup mode + opener origin are read up front so even early failures can
	// report back through the popup channel.
	pre := oauthState{
		Popup:  r.URL.Query().Get("popup") == "1",
		Origin: allowedOrigin(r.URL.Query().Get("origin")),
	}

	clientID, _, err := s.clientCreds(r.Context())
	if err != nil {
		logging.Error(packageName, "Start: creds: %v", err)
		s.fail(w, r, pre, "/login", "Microsoft sign-in is temporarily unavailable. Please try again or use email.")
		return
	}

	nonce, err := randHex(16)
	if err != nil {
		s.fail(w, r, pre, "/login", "Couldn't start Microsoft sign-in. Please try again.")
		return
	}
	st := oauthState{
		Nonce:  nonce,
		Plan:   strings.TrimSpace(r.URL.Query().Get("plan")),
		Cycle:  strings.TrimSpace(r.URL.Query().Get("cycle")),
		Mode:   strings.TrimSpace(r.URL.Query().Get("mode")),
		Popup:  pre.Popup,
		Origin: pre.Origin,
	}
	enc, err := encodeState(st)
	if err != nil {
		s.fail(w, r, pre, "/login", "Couldn't start Microsoft sign-in. Please try again.")
		return
	}

	// Lax (not Strict) so the cookie survives the top-level redirect back from
	// login.microsoftonline.com; short-lived; httpOnly + Secure.
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: nonce, Path: "/",
		MaxAge: 600, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", s.redirect)
	q.Set("response_type", "code")
	q.Set("response_mode", "query")
	q.Set("scope", "openid email profile")
	q.Set("state", enc)
	q.Set("prompt", "select_account")
	http.Redirect(w, r, microsoftAuthURL+"?"+q.Encode(), http.StatusSeeOther)
}

// Callback handles Microsoft's redirect back: verify state, exchange the code,
// read the identity, then log in or start the signup funnel.
func (s *Service) Callback(w http.ResponseWriter, r *http.Request) {
	// Verify state first: decrypt the param and match its nonce to the cookie.
	st, err := decodeState(r.URL.Query().Get("state"))
	cookie, cErr := r.Cookie(stateCookie)
	clearStateCookie(w)
	if err != nil || cErr != nil || st.Nonce == "" || st.Nonce != cookie.Value {
		logging.Error(packageName, "Callback: state mismatch (err=%v cookieErr=%v)", err, cErr)
		flash(w, r, "/login", "Your Microsoft sign-in session expired. Please try again.")
		return
	}

	// Microsoft bounced an error (e.g. user cancelled / consent declined).
	if e := r.URL.Query().Get("error"); e != "" {
		s.fail(w, r, st, "/login", "Microsoft sign-in was cancelled.")
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		s.fail(w, r, st, "/login", "Couldn't complete Microsoft sign-in. Please try again.")
		return
	}

	claims, err := s.exchange(r.Context(), code)
	if err != nil {
		logging.Error(packageName, "Callback: token exchange: %v", err)
		s.fail(w, r, st, "/login", "Couldn't complete Microsoft sign-in. Please try again.")
		return
	}
	// Microsoft's id_token carries no `email_verified` claim. We take the
	// account address from `email`, falling back to `preferred_username` (the
	// UPN, which is email-shaped). For standard MSA and Entra accounts this is
	// the address the user authenticated against and proved control of to
	// Microsoft, so we treat it as verified — the same posture every "Sign in
	// with Microsoft" integration takes.
	email := claims.email()
	if email == "" {
		s.fail(w, r, st, "/login", "Your Microsoft account didn't return an email address, so we can't sign you in with it.")
		return
	}

	// Re-auth (step-up) mode: confirm a sensitive action (account deletion)
	// without creating a session — verify the Microsoft email matches the
	// current user and hand back a short-lived step-up token via the popup.
	if st.Mode == "reauth" {
		s.handleReauth(w, r, st, email)
		return
	}

	// Existing account → log straight in, but ONLY when the Microsoft identity
	// matches the account's stored binding. Microsoft asserts no email_verified
	// claim, so an email match alone is NOT trusted (emailVerified=false) — that
	// is what blocks a forged-UPN takeover of an account not linked to this
	// Microsoft identity. ResolveOAuthLogin links unbound accounts on first use
	// only for providers that DO verify the email, so Microsoft never auto-links.
	uid, ok, err := s.auth.ResolveOAuthLogin(r.Context(), "microsoft", claims.subject(), email, false)
	switch {
	case errors.Is(err, auth.ErrOAuthIdentityMismatch),
		errors.Is(err, auth.ErrOAuthDifferentProvider),
		errors.Is(err, auth.ErrOAuthUnverified):
		logging.Info(packageName, "Callback: Microsoft login refused for %s: %v", email, err)
		s.fail(w, r, st, "/login", "We couldn't sign you into that account with Microsoft. Please sign in with your email and password.")
		return
	case err != nil:
		logging.Error(packageName, "Callback: resolve login: %v", err)
		s.fail(w, r, st, "/login", "Couldn't complete Microsoft sign-in. Please try again.")
		return
	}
	if ok {
		// Microsoft verified the identity (first factor). If the user enrolled
		// app-level 2FA, we STILL require it — Microsoft replaces the password,
		// not the second factor — UNLESS this device was previously trusted
		// (same rule + cookie as password login, for consistency).
		var trustedDevice string
		if c, cErr := r.Cookie(auth.TrustedDeviceCookieName); cErr == nil {
			trustedDevice = c.Value
		}
		res, err := s.auth.DecideOAuthLogin(r.Context(), uid, auth.ClientIP(r), r.UserAgent(), trustedDevice)
		if err != nil {
			logging.Error(packageName, "Callback: decide login for %s: %v", email, err)
			s.fail(w, r, st, "/login", "Couldn't sign you in. Please try again.")
			return
		}
		if res.TwoFARequired {
			set2FAPending(w, res.PendingToken, res.Method)
			s.succeed(w, r, st, "/login")
			return
		}
		// No 2FA — multi-tenant users land on the account selector first;
		// single-tenant users go straight to /home (mirrors Google + password).
		session.SetCookie(w, r, res.Identity.Session.ID, res.Identity.Session.ExpiresAt)
		dest := "/home"
		if ts, terr := s.auth.ListTenantsForUser(r.Context(), uid); terr == nil && len(ts) > 1 {
			dest = "/select-account"
		}
		s.succeed(w, r, st, dest)
		return
	}

	// New email. Without a chosen plan (e.g. clicked Microsoft on the login
	// page), they have no account yet — send them to the plans page to convert.
	// They'll pick a plan and continue with Microsoft from there (we already
	// know their identity, so it's a one-click finish).
	if st.Plan == "" {
		s.noAccount(w, r, st)
		return
	}

	intent, err := s.intents.CreateOAuth(r.Context(), email, st.Plan, st.Cycle, "microsoft", claims.subject(), claims.displayName())
	if errors.Is(err, signupintents.ErrEmailRegistered) {
		s.fail(w, r, st, "/login", "You already have an account — please sign in.")
		return
	}
	if err != nil {
		logging.Error(packageName, "Callback: create oauth intent: %v", err)
		s.fail(w, r, st, "/signup", "Couldn't start your Microsoft sign-up. Please try again.")
		return
	}
	// Straight to payment — Microsoft authenticated the email.
	s.succeed(w, r, st, "/signup/checkout/"+intent.Token)
}

// ----- Microsoft token exchange --------------------------------------------

type microsoftClaims struct {
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Sub               string `json:"sub"` // pairwise, stable per (user, app)
	Oid               string `json:"oid"` // tenant object id (stable across apps)
	Tid               string `json:"tid"` // tenant id
}

// email returns the best verified address: the `email` claim, else the UPN
// (`preferred_username`) when it's email-shaped.
func (c *microsoftClaims) email() string {
	if e := strings.ToLower(strings.TrimSpace(c.Email)); e != "" {
		return e
	}
	if u := strings.ToLower(strings.TrimSpace(c.PreferredUsername)); strings.Contains(u, "@") {
		return u
	}
	return ""
}

// subject is the durable provider identity link. `sub` is stable per (user,
// app); `oid` is stable across apps in a tenant — prefer oid when present.
func (c *microsoftClaims) subject() string {
	if c.Oid != "" {
		return c.Oid
	}
	return c.Sub
}

func (c *microsoftClaims) displayName() string {
	return strings.TrimSpace(c.Name)
}

func (s *Service) exchange(ctx context.Context, code string) (*microsoftClaims, error) {
	clientID, clientSecret, err := s.clientCreds(ctx)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("redirect_uri", s.redirect)
	form.Set("grant_type", "authorization_code")
	form.Set("scope", "openid email profile")

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, microsoftTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, string(body))
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if tok.IDToken == "" {
		return nil, fmt.Errorf("no id_token in response")
	}
	return decodeIDToken(tok.IDToken)
}

// decodeIDToken reads the claims from a JWT's payload. Signature verification is
// unnecessary: the token came over TLS directly from Microsoft's token endpoint.
func decodeIDToken(idToken string) (*microsoftClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed id_token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode id_token payload: %w", err)
	}
	var c microsoftClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("parse id_token claims: %w", err)
	}
	return &c, nil
}

// ----- state helpers --------------------------------------------------------

func encodeState(st oauthState) (string, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	enc, err := crypto.Encrypt(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(enc), nil
}

func decodeState(s string) (oauthState, error) {
	var st oauthState
	raw, err := hex.DecodeString(s)
	if err != nil {
		return st, err
	}
	plain, err := crypto.Decrypt(raw)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(plain, &st); err != nil {
		return st, err
	}
	return st, nil
}

func clearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
