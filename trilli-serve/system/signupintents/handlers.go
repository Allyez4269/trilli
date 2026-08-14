package signupintents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"trilli/system/auth"
	"trilli/system/billing"
	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/ratelimit"
	"trilli/system/session"
)

const appBase = "https://app.trilli.com"

// Handlers orchestrates the pay-before-account funnel across the intent store,
// Stripe (hosted Checkout), the mailer, and account provisioning.
type Handlers struct {
	svc     *Service
	billing *billing.Service
	auth    *auth.Service
	mailer  *mailer.Mailer // may be nil
}

func NewHandlers(svc *Service, b *billing.Service, a *auth.Service, m *mailer.Mailer) *Handlers {
	return &Handlers{svc: svc, billing: b, auth: a, mailer: m}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// signupCORSOrigins are the marketing-site origins allowed to call the funnel's
// entry endpoint cross-origin (the "Get Trilli" modal lives on www.trilli.com,
// which is a separate deployment from app.trilli.com). Only /api/signup/start is
// invoked from there; the rest run same-origin on the app after the email link.
var signupCORSOrigins = map[string]bool{
	"https://www.trilli.com": true,
	"https://trilli.com":     true,
}

// cors wraps the marketing-facing endpoint with an origin-checked CORS policy
// and answers the preflight. Unknown origins simply get no CORS headers.
func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); signupCORSOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

// Register wires the public funnel endpoints (no session required — these run
// before an account exists). /api/signup/start is CORS-enabled + has a preflight
// route so the marketing-site modal can call it cross-origin.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	start := cors(h.Start)
	m.Handle("POST /api/signup/start", http.HandlerFunc(start))
	m.Handle("OPTIONS /api/signup/start", http.HandlerFunc(start))
	m.Handle("POST /api/signup/verify", http.HandlerFunc(h.Verify))
	m.Handle("GET /api/signup/intent", http.HandlerFunc(h.Intent))
	m.Handle("GET /api/signup/checkout", http.HandlerFunc(h.Checkout))
	m.Handle("POST /api/signup/finalize", http.HandlerFunc(h.Finalize))
	m.Handle("POST /api/signup/complete", http.HandlerFunc(h.Complete))
	// Authenticated "buy a second account" — reuses the checkout/finalize flow
	// above, then provisions a new tenant for the existing user.
	m.Handle("POST /api/account/new", requireAuth(http.HandlerFunc(h.NewAccountStart)))
	m.Handle("POST /api/account/new/complete", requireAuth(http.HandlerFunc(h.NewAccountComplete)))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

// splitName splits a display name into first + last (the rest). Used to seed
// the account name from an OAuth provider's profile when the funnel form didn't
// collect one.
func splitName(full string) (first, last string) {
	parts := strings.Fields(strings.TrimSpace(full))
	if len(parts) == 0 {
		return "", ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

func confirmLink(token string) string  { return appBase + "/signup/verify/" + token }
func completeLink(token string) string { return appBase + "/signup/complete/" + token }

// Start (POST /api/signup/start) begins the email-path funnel: it creates a
// pending intent and emails a confirmation link. If the email already maps to a
// real account it returns {registered:true} so the UI routes to /login.
// startLimiter throttles funnel starts per IP (email sends + account-existence
// probing).
var startLimiter = ratelimit.PerMinute(5, 10)

func (h *Handlers) Start(w http.ResponseWriter, r *http.Request) {
	// Throttled: each accepted call sends a confirmation email and reveals
	// whether the address already has an account.
	if !startLimiter.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	var req struct {
		Email   string `json:"email"`
		Plan    string `json:"plan"`
		Billing string `json:"billing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	intent, err := h.svc.Create(r.Context(), req.Email, req.Plan, req.Billing)
	if errors.Is(err, ErrEmailRegistered) {
		writeJSON(w, http.StatusOK, map[string]any{"registered": true})
		return
	}
	if err != nil {
		logging.Error(packageName, "Start failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not start signup"})
		return
	}
	if h.mailer != nil {
		if err := h.mailer.SendSignupConfirm(r.Context(), mailer.SignupConfirmEmail{
			To: intent.Email, Link: confirmLink(intent.Token),
		}); err != nil {
			logging.Error(packageName, "confirm email send failed for %s: %v", intent.Email, err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": intent.Email})
}

// Verify (POST /api/signup/verify) consumes the email-confirmation token and
// opens hosted Checkout, returning the URL for the browser to redirect to. If
// the intent is already paid it points the UI to Step 6 instead.
func (h *Handlers) Verify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	intent, err := h.svc.Verify(r.Context(), strings.TrimSpace(req.Token))
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "This link has expired. Please start again."})
		return
	}
	if err != nil {
		logging.Error(packageName, "Verify failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification failed"})
		return
	}
	// Already paid (re-click) — send them straight to finish setup.
	if intent.Status == StatusPaid || intent.Status == StatusCompleted {
		writeJSON(w, http.StatusOK, map[string]any{"next": "/signup/complete/" + intent.Token})
		return
	}
	// Email verified → our own embedded-Elements checkout page (subscription is
	// created lazily there). The actual Stripe Checkout is no longer hosted.
	writeJSON(w, http.StatusOK, map[string]any{"next": "/signup/checkout/" + intent.Token})
}

// Checkout (GET /api/signup/checkout?token=) returns the embedded-Elements
// payment context for the funnel: it creates (or reuses) a default-incomplete
// Stripe subscription for the chosen plan and returns its confirmation secret +
// publishable key. The selected-plan card is rendered client-side from /api/plans.
func (h *Handlers) Checkout(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	intent, err := h.svc.GetByToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if intent.Status == StatusCompleted {
		writeJSON(w, http.StatusOK, map[string]any{"status": "completed", "for_existing_user": intent.UserID.Valid})
		return
	}
	if intent.Status == StatusPaid {
		writeJSON(w, http.StatusOK, map[string]any{"status": "paid", "for_existing_user": intent.UserID.Valid})
		return
	}
	if intent.Status != StatusEmailVerified {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Please confirm your email first."})
		return
	}

	var clientSecret string
	var amount int64
	if intent.StripeSubscriptionID != "" {
		// Reuse the existing subscription (page reload) rather than duplicate it.
		cs, amt, subStatus, err := h.billing.SignupSubClientSecret(intent.StripeSubscriptionID)
		if err != nil {
			logging.Error(packageName, "reuse signup sub failed: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not load checkout"})
			return
		}
		if subStatus == "active" || subStatus == "trialing" {
			writeJSON(w, http.StatusOK, map[string]any{"status": "paid", "for_existing_user": intent.UserID.Valid})
			return
		}
		clientSecret, amount = cs, amt
	} else {
		res, err := h.billing.CreateSignupSubscription(r.Context(), intent.Token, intent.Email, intent.PlanCode, intent.BillingCycle)
		if err != nil {
			logging.Error(packageName, "CreateSignupSubscription failed: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not start checkout"})
			return
		}
		if err := h.svc.SetStripeRefs(r.Context(), intent.ID, res.CustomerID, res.SubscriptionID); err != nil {
			logging.Error(packageName, "SetStripeRefs failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		clientSecret, amount = res.ClientSecret, res.Amount
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ready",
		"client_secret":   clientSecret,
		"publishable_key": h.billing.PublishableKey(),
		"amount":          amount,
		"email":           intent.Email,
		"plan_code":       intent.PlanCode,
		"billing_cycle":   intent.BillingCycle,
		// Drives the step label: OAuth signups authenticated via a provider
		// instead of confirming an email, so step 1 reads "User authentication".
		"oauth_provider": intent.OAuthProvider,
		// True ⇒ an authenticated user buying a SECOND account: after payment the
		// client routes to /account/new/finish (not the cold-funnel /signup/complete).
		"for_existing_user": intent.UserID.Valid,
	})
}

// Finalize (POST /api/signup/finalize) is called right after the Payment
// Element confirms — it verifies the subscription is paid (synchronously, not
// waiting on the webhook) and marks the intent paid so Step 6 can proceed.
func (h *Handlers) Finalize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	intent, err := h.svc.GetByToken(r.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if intent.Status == StatusPaid || intent.Status == StatusCompleted {
		writeJSON(w, http.StatusOK, map[string]any{"paid": true})
		return
	}
	if intent.StripeSubscriptionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no checkout in progress"})
		return
	}
	paid, err := h.billing.SignupSubPaid(intent.StripeSubscriptionID)
	if err != nil {
		logging.Error(packageName, "SignupSubPaid failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not verify payment"})
		return
	}
	if !paid {
		writeJSON(w, http.StatusOK, map[string]any{"paid": false})
		return
	}
	if _, err := h.svc.MarkPaidByToken(r.Context(), intent.Token, intent.StripeSubscriptionID, intent.StripeCustomerID); err != nil {
		logging.Error(packageName, "MarkPaidByToken failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paid": true})
}

// OnSignupSubActivated is the billing SignupSubActivatedHook: the webhook backup
// that marks an embedded funnel intent paid. The "finish setting up" resume
// email is NOT sent here — the webhook fires within seconds of payment, while
// the buyer is typically still on the account-creation step, so sending now
// would mail everyone (completers included). It's deferred to the janitor
// (SweepResumeReminders), which only mails intents still 'paid' past a grace.
func (h *Handlers) OnSignupSubActivated(ctx context.Context, token, subscriptionID, customerID string) error {
	_, err := h.svc.MarkPaidByToken(ctx, token, subscriptionID, customerID)
	return err
}

// SweepResumeReminders mails the "finish setting up" reminder to buyers who paid
// but haven't completed account creation within `grace`. This is the DEFERRED
// replacement for sending it from the payment webhook (which fired seconds after
// payment, while the buyer was still on the account-creation step, so completers
// got it too). Send-then-mark: a single janitor runs this so there's no
// double-send, and a transient mail failure simply retries on the next sweep.
func (h *Handlers) SweepResumeReminders(ctx context.Context, grace time.Duration) (int, error) {
	if h.mailer == nil {
		return 0, nil
	}
	cands, err := h.svc.PaidAwaitingResume(ctx, grace)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, c := range cands {
		if err := h.mailer.SendSignupResume(ctx, mailer.SignupResumeEmail{
			To: c.Email, Link: completeLink(c.Token),
		}); err != nil {
			logging.Error(packageName, "resume reminder send failed for %s: %v", c.Email, err)
			continue // leave it unmarked → retried next sweep
		}
		if err := h.svc.MarkResumeEmailed(ctx, c.ID); err != nil {
			logging.Error(packageName, "resume reminder mark failed id=%d: %v", c.ID, err)
		}
		sent++
	}
	if sent > 0 {
		logging.Info(packageName, "sent %d signup resume reminder(s)", sent)
	}
	return sent, nil
}

// Intent (GET /api/signup/intent?token=) returns the public-safe state the
// verify-landing + Step-6 pages need to render.
func (h *Handlers) Intent(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	intent, err := h.svc.GetByToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"email":               intent.Email,
		"plan_code":           intent.PlanCode,
		"billing_cycle":       intent.BillingCycle,
		"status":              intent.Status,
		"account_type":        intent.AccountType,
		"oauth_provider":      intent.OAuthProvider,
		"full_name":           intent.FullName,
		"is_comp":             intent.IsComp,
		"comp_free_term_days": intent.CompFreeTermDays,
	})
}

// Complete (POST /api/signup/complete) is Step 6: with payment secured, it
// provisions the account and logs the user in.
func (h *Handlers) Complete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token           string `json:"token"`
		AccountType     string `json:"account_type"`
		BusinessName    string `json:"business_name"`
		FirstName       string `json:"first_name"`
		LastName        string `json:"last_name"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirm_password"`
		Address         struct {
			Line1, Line2, City, State, PostalCode, Country string
		} `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	intent, err := h.svc.GetByToken(r.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if intent.Status == StatusCompleted {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This account is already set up. Please sign in."})
		return
	}
	switch {
	case intent.IsComp && intent.Status != StatusEmailVerified:
		// A comp invite is ready to complete only while live (email_verified);
		// cancelled/expired links are dead.
		writeJSON(w, http.StatusGone, map[string]string{"error": "This invite has expired or already been used."})
		return
	case !intent.IsComp && intent.Status != StatusPaid:
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": "Payment hasn't completed yet."})
		return
	}
	// OAuth signups (Google/Microsoft) have no password — the name comes from
	// the provider when the form didn't collect one. Password-path signups must
	// still confirm their password.
	oauth := intent.OAuthProvider != ""
	if !oauth && req.Password != req.ConfirmPassword {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Passwords don't match."})
		return
	}
	firstName, lastName := req.FirstName, req.LastName
	if oauth && strings.TrimSpace(firstName+lastName) == "" {
		firstName, lastName = splitName(intent.FullName)
	}

	tenantName := strings.TrimSpace(req.BusinessName)
	if strings.ToLower(strings.TrimSpace(req.AccountType)) != "business" || tenantName == "" {
		first := strings.TrimSpace(firstName)
		if first != "" {
			tenantName = first + "'s Trilli"
		} else {
			tenantName = "My Trilli"
		}
	}

	identity, err := h.auth.SignupFromIntent(r.Context(), auth.SignupFromIntentInput{
		Email:          intent.Email,
		Password:       req.Password,
		FirstName:      firstName,
		LastName:       lastName,
		TenantName:     tenantName,
		PlanCode:       intent.PlanCode,
		BillingPeriod:  intent.BillingCycle,
		StripeCustomer: intent.StripeCustomerID,
		StripeSub:      intent.StripeSubscriptionID,
		OAuthProvider:  intent.OAuthProvider,
		OAuthSubject:   intent.OAuthSubject,
	}, auth.ClientIP(r), r.UserAgent())
	if errors.Is(err, auth.ErrEmailTaken) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "An account with this email already exists. Please sign in."})
		return
	}
	if errors.Is(err, auth.ErrPasswordTooShort) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Password must be at least 8 characters."})
		return
	}
	if err != nil {
		logging.Error(packageName, "SignupFromIntent failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create account"})
		return
	}

	if intent.IsComp {
		// Comp account: no Stripe. Flag the tenant as a comp account with its
		// granted term and record which tenant this invite provisioned.
		if err := h.svc.MarkTenantComp(r.Context(), identity.Tenant.ID, intent.CompFreeTermDays); err != nil {
			logging.Error(packageName, "mark tenant comp failed: %v", err)
		}
		if err := h.svc.SetCompTenant(r.Context(), intent.ID, identity.Tenant.ID); err != nil {
			logging.Error(packageName, "set comp tenant failed: %v", err)
		}
	} else {
		// Stamp tenant_id onto the subscription so renewal/cancel webhooks route,
		// and persist the (possibly edited) billing address to the Stripe customer.
		if err := h.billing.StampSubscriptionTenant(r.Context(), intent.StripeSubscriptionID, identity.Tenant.ID); err != nil {
			logging.Error(packageName, "stamp subscription tenant failed: %v", err)
		}
		if err := h.billing.UpdateCustomerAddress(r.Context(), intent.StripeCustomerID,
			strings.TrimSpace(firstName+" "+lastName), billing.Address{
				Line1: req.Address.Line1, Line2: req.Address.Line2, City: req.Address.City,
				State: req.Address.State, PostalCode: req.Address.PostalCode, Country: req.Address.Country,
			}); err != nil {
			logging.Error(packageName, "update customer address failed: %v", err)
		}
	}
	if err := h.svc.MarkCompleted(r.Context(), intent.ID); err != nil {
		logging.Error(packageName, "MarkCompleted failed: %v", err)
	}

	// Welcome aboard — best effort, never blocks the response.
	if h.mailer != nil {
		if err := h.mailer.SendWelcome(r.Context(), mailer.WelcomeEmail{
			To: intent.Email, FirstName: firstName, Link: appBase + "/home",
		}); err != nil {
			logging.Error(packageName, "welcome email send failed for %s: %v", intent.Email, err)
		}
	}

	session.SetCookie(w, r, identity.Session.ID, identity.Session.ExpiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{
		"user":       identity.User,
		"tenant":     identity.Tenant,
		"membership": identity.Membership,
	})
}

// NewAccountStart begins an AUTHENTICATED "buy a second account" purchase for
// the signed-in user: creates a pre-verified intent bound to their user_id and
// returns the token, which drives the same checkout/finalize flow as the cold
// funnel. POST /api/account/new {plan_code, billing_cycle}.
func (h *Handlers) NewAccountStart(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		PlanCode     string `json:"plan_code"`
		BillingCycle string `json:"billing_cycle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.PlanCode) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	intent, err := h.svc.CreateForUser(r.Context(), identity.User.ID, identity.User.Email, req.PlanCode, req.BillingCycle)
	if err != nil {
		logging.Error(packageName, "CreateForUser failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not start"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": intent.Token})
}

// NewAccountComplete finishes an authenticated second-account purchase: it
// provisions a NEW tenant owned by the signed-in user (reusing their identity —
// no new user row), switches their session into the new account, and returns
// the new tenant id. POST /api/account/new/complete {token, account_type,
// business_name, address}.
func (h *Handlers) NewAccountComplete(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.User == nil || identity.Session == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Token        string `json:"token"`
		AccountType  string `json:"account_type"`
		BusinessName string `json:"business_name"`
		Address      struct {
			Line1, Line2, City, State, PostalCode, Country string
		} `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	intent, err := h.svc.GetByToken(r.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// The intent MUST belong to this user (bound at NewAccountStart) and be paid.
	if !intent.UserID.Valid || intent.UserID.Int64 != identity.User.ID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if intent.Status == StatusCompleted {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This account is already set up."})
		return
	}
	if intent.Status != StatusPaid {
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": "Payment hasn't completed yet."})
		return
	}

	first := ""
	if identity.User.FirstName != nil {
		first = strings.TrimSpace(*identity.User.FirstName)
	}
	tenantName := strings.TrimSpace(req.BusinessName)
	if tenantName == "" {
		if first != "" {
			tenantName = first + "'s Trilli"
		} else {
			tenantName = "My Trilli"
		}
	}

	tenant, err := h.auth.ProvisionOwnedTenant(r.Context(), auth.NewOwnedTenantInput{
		UserID:         identity.User.ID,
		TenantName:     tenantName,
		PlanCode:       intent.PlanCode,
		BillingPeriod:  intent.BillingCycle,
		StripeCustomer: intent.StripeCustomerID,
		StripeSub:      intent.StripeSubscriptionID,
	})
	if err != nil {
		logging.Error(packageName, "ProvisionOwnedTenant failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create account"})
		return
	}
	if err := h.billing.StampSubscriptionTenant(r.Context(), intent.StripeSubscriptionID, tenant.ID); err != nil {
		logging.Error(packageName, "stamp subscription tenant failed: %v", err)
	}
	name := ""
	if identity.User.FullName != nil {
		name = strings.TrimSpace(*identity.User.FullName)
	}
	if err := h.billing.UpdateCustomerAddress(r.Context(), intent.StripeCustomerID, name, billing.Address{
		Line1: req.Address.Line1, Line2: req.Address.Line2, City: req.Address.City,
		State: req.Address.State, PostalCode: req.Address.PostalCode, Country: req.Address.Country,
	}); err != nil {
		logging.Error(packageName, "update customer address failed: %v", err)
	}
	if err := h.svc.MarkCompleted(r.Context(), intent.ID); err != nil {
		logging.Error(packageName, "MarkCompleted failed: %v", err)
	}

	// Re-scope the existing session into the brand-new account so the user lands
	// in it. Best-effort: if it fails, the account still exists and the user can
	// pick it from the account selector.
	if _, err := h.auth.SwitchTenant(r.Context(), identity.Session.ID, identity.User.ID, tenant.ID); err != nil {
		logging.Error(packageName, "switch to new tenant failed: %v", err)
	}
	logging.Info(packageName, "second account provisioned: user=%d tenant=%d", identity.User.ID, tenant.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tenant_id": tenant.ID})
}

// OnCheckoutPaid is the billing SignupPaidHook: a completed Checkout marks the
// intent paid (authoritative). Account provisioning happens at Complete (Step 6).
// The resume email is deferred to the janitor (SweepResumeReminders) so prompt
// completers don't receive it — see OnSignupSubActivated.
func (h *Handlers) OnCheckoutPaid(ctx context.Context, token, sessionID, subscriptionID, customerID string) error {
	_, err := h.svc.MarkPaid(ctx, sessionID, subscriptionID, customerID)
	return err
}
