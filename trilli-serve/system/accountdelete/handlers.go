// Package accountdelete wires owner-initiated account deletion and its reversal.
//
// Deletion is a recoverable, 30-day soft close: the tenant enters
// lifecycle_state='closing' (read-only, like a lapsed account) and its data is
// purged by the existing lifecycle sweep once purge_at passes. The owner can
// Reactivate anytime in the window. Both actions are gated behind a step-up
// re-auth — a passkey assertion, or password (+ a TOTP code when 2FA is on) —
// mirroring the password-reset flow so OAuth/2FA users are covered.
//
// Billing effects (cancel-at-period-end, the <30-day full refund, and the
// reactivation re-charge) are layered on top via the billing service.
package accountdelete

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"trilli/system/audit"
	"trilli/system/auth"
	"trilli/system/billing"
	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/passkeys"
	"trilli/system/ratelimit"
	"trilli/system/twofactor"
)

const packageName = "accountdelete"

// passkeyStateHeader mirrors the header the passkeys package reads its sealed
// challenge from.
const passkeyStateHeader = "X-Passkey-State"

// RefundWindow is how new an account may be (age since creation) to qualify for
// a full money-back refund on deletion. Matches the lapse grace window.
const RefundWindow = 30 * 24 * time.Hour

// Handlers wires the dependencies for the delete/reactivate flow.
type Handlers struct {
	authSvc  *auth.Service
	billing  *billing.Service
	twofa    *twofactor.Service
	passkeys *passkeys.Service
	audit    *audit.Service
	mailer   *mailer.Mailer
}

// NewHandlers constructs the account-deletion handlers.
func NewHandlers(a *auth.Service, b *billing.Service, t *twofactor.Service, p *passkeys.Service, au *audit.Service, m *mailer.Mailer) *Handlers {
	return &Handlers{authSvc: a, billing: b, twofa: t, passkeys: p, audit: au, mailer: m}
}

// firstName safely dereferences the user's first name for email greetings.
func firstName(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the routes — all require a session; the actions additionally
// require the caller to be the account owner.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("GET /api/account/delete/preview", requireAuth(http.HandlerFunc(h.Preview)))
	m.Handle("POST /api/account/delete", requireAuth(http.HandlerFunc(h.Delete)))
	m.Handle("POST /api/account/delete/passkey/begin", requireAuth(http.HandlerFunc(h.PasskeyBegin)))
	m.Handle("POST /api/account/reactivate", requireAuth(http.HandlerFunc(h.Reactivate)))
}

// Preview returns the consequences of deleting the current account, so the
// modal can show accurate copy: storage + member count, whether deletion is
// inside the money-back window, the exact refund amount, and whether a live
// subscription will be cancelled. Owner-only (matches the action it previews).
func (h *Handlers) Preview(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if identity.Membership == nil || identity.Membership.Role != "owner" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Only the account owner can delete the account."})
		return
	}
	refundCents, hasActiveSub, err := h.billing.DeletionBillingPreview(r.Context(), identity.Tenant.ID, identity.Tenant.CreatedAt, RefundWindow)
	if err != nil {
		logging.Error(packageName, "Preview: billing preview tenant=%d: %v", identity.Tenant.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account_name":       identity.Tenant.Name,
		"storage_bytes":      identity.Tenant.StorageBytesUsed,
		"member_count":       identity.Tenant.UserCount,
		"refund_eligible":    time.Since(identity.Tenant.CreatedAt) < RefundWindow,
		"refund_cents":       refundCents,
		"has_active_billing": hasActiveSub,
		"grace_days":         int(RefundWindow / (24 * time.Hour)),
	})
}

var stepUpLimiter = ratelimit.PerMinute(6, 10)

// PasskeyBegin returns a passkey assertion challenge scoped to the caller, for
// the passkey step-up branch of Delete.
func (h *Handlers) PasskeyBegin(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	assertion, state, err := h.passkeys.BeginStepUp(r.Context(), identity.User.ID)
	if err != nil {
		logging.Error(packageName, "PasskeyBegin failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicKey": assertion.Response, "state": state})
}

// Delete soft-closes the caller's current account after a step-up re-auth.
// Owner-only. Sets lifecycle_state='closing' with a 30-day purge window; the
// billing effects (cancel-at-period-end + <30-day refund) run here too.
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	if !stepUpLimiter.Allow(ratelimit.ClientIP(r)) {
		ratelimit.TooMany(w)
		return
	}
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if identity.Membership == nil || identity.Membership.Role != "owner" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Only the account owner can delete the account."})
		return
	}
	if identity.Tenant.LifecycleState == "closing" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This account is already scheduled for deletion."})
		return
	}
	if identity.Tenant.LifecycleState != "current" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This account can't be deleted in its current state."})
		return
	}

	if err := h.verifyStepUp(w, r, identity.User.ID); err != nil {
		return // verifyStepUp already wrote the response
	}

	tenantID := identity.Tenant.ID

	// Billing: cancel at period end, and (if the account is < 30 days old) issue
	// a full refund now. CloseBilling returns how much was refunded so
	// reactivation knows what to re-charge. Best-effort on a billing-less setup.
	refundCents, err := h.billing.CloseBilling(r.Context(), tenantID, identity.Tenant.CreatedAt, RefundWindow)
	if err != nil {
		logging.Error(packageName, "Delete: close billing tenant=%d: %v", tenantID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "We couldn't process the billing side of this deletion. No changes were made — please try again."})
		return
	}

	if err := h.billing.MarkClosing(r.Context(), tenantID, refundCents); err != nil {
		logging.Error(packageName, "Delete: mark closing tenant=%d: %v", tenantID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	h.audit.Record(r.Context(), audit.RecordInput{
		TenantID:    tenantID,
		ActorUserID: identity.User.ID,
		ActorEmail:  identity.User.Email,
		Action:      audit.ActionAccountDelete,
		TargetType:  audit.TargetAccount,
		ItemName:    identity.Tenant.Name,
		Device:      audit.DeviceFromUA(r.UserAgent()),
		IP:          auth.ClientIP(r),
	})

	purgeAt := time.Now().Add(RefundWindow)
	graceDays := int(RefundWindow / (24 * time.Hour))

	// Confirmation email (best-effort — the deletion already succeeded).
	if h.mailer != nil {
		if err := h.mailer.SendAccountCloseConfirm(r.Context(), mailer.AccountCloseConfirmEmail{
			To:          identity.User.Email,
			Name:        firstName(identity.User.FirstName),
			AccountName: identity.Tenant.Name,
			PurgeDate:   purgeAt.Format("January 2, 2006"),
			DaysLeft:    graceDays,
			RefundCents: refundCents,
		}); err != nil {
			logging.Error(packageName, "Delete: send confirm email tenant=%d: %v", tenantID, err)
		}
	}

	logging.Info(packageName, "Delete: tenant=%d by user=%d refund_cents=%d", tenantID, identity.User.ID, refundCents)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"purge_at":     purgeAt.UTC().Format(time.RFC3339),
		"refund_cents": refundCents,
	})
}

// Reactivate cancels a pending deletion within the grace window. Owner-only.
// If a refund was issued at deletion, the account is re-charged first; a failed
// charge leaves the account closing and returns 402 so the UI can prompt for a
// payment method.
func (h *Handlers) Reactivate(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if identity.Membership == nil || identity.Membership.Role != "owner" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Only the account owner can reactivate the account."})
		return
	}
	if identity.Tenant.LifecycleState != "closing" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This account isn't scheduled for deletion."})
		return
	}
	tenantID := identity.Tenant.ID

	// Capture the refunded amount before ReopenBilling/ClearClosing zero it, so
	// the confirmation email can tell the owner what was re-charged.
	rechargeCents, _ := h.billing.ClosingRefundCents(r.Context(), tenantID)

	// Re-establish billing (un-cancel the sub; re-charge a refunded account).
	if err := h.billing.ReopenBilling(r.Context(), tenantID); err != nil {
		if errors.Is(err, billing.ErrNoPaymentMethod) {
			writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": "Add a payment method to reactivate this account."})
			return
		}
		logging.Error(packageName, "Reactivate: reopen billing tenant=%d: %v", tenantID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "We couldn't restart billing for this account. Please try again."})
		return
	}

	if err := h.billing.ClearClosing(r.Context(), tenantID); err != nil {
		logging.Error(packageName, "Reactivate: clear closing tenant=%d: %v", tenantID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	h.audit.Record(r.Context(), audit.RecordInput{
		TenantID:    tenantID,
		ActorUserID: identity.User.ID,
		ActorEmail:  identity.User.Email,
		Action:      audit.ActionAccountReactivate,
		TargetType:  audit.TargetAccount,
		ItemName:    identity.Tenant.Name,
		Device:      audit.DeviceFromUA(r.UserAgent()),
		IP:          auth.ClientIP(r),
	})

	if h.mailer != nil {
		if err := h.mailer.SendAccountReactivated(r.Context(), mailer.AccountReactivatedEmail{
			To:             identity.User.Email,
			Name:           firstName(identity.User.FirstName),
			AccountName:    identity.Tenant.Name,
			RechargedCents: rechargeCents,
		}); err != nil {
			logging.Error(packageName, "Reactivate: send email tenant=%d: %v", tenantID, err)
		}
	}

	logging.Info(packageName, "Reactivate: tenant=%d by user=%d", tenantID, identity.User.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// verifyStepUp enforces the re-auth: a passkey assertion (when the
// X-Passkey-State header is present), otherwise password — plus a TOTP code
// when 2FA is enabled. Writes the error response and returns non-nil on failure.
func (h *Handlers) verifyStepUp(w http.ResponseWriter, r *http.Request, userID int64) error {
	if state := r.Header.Get(passkeyStateHeader); state != "" {
		if err := h.passkeys.VerifyStepUp(r.Context(), userID, state, r); err != nil {
			writeStepUpErr(w, err)
			return err
		}
		return nil
	}

	var req struct {
		Password    string `json:"password"`
		Code        string `json:"code"`
		ReauthToken string `json:"reauth_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return err
	}

	// OAuth re-auth path: a short-lived step-up token minted when the user just
	// re-authenticated via Google/Microsoft. Must be bound to THIS user.
	if req.ReauthToken != "" {
		uid, terr := h.authSvc.ParseStepUpToken(req.ReauthToken)
		if terr != nil || uid != userID {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Re-authentication expired. Please confirm again."})
			if terr == nil {
				terr = errors.New("step-up token user mismatch")
			}
			return terr
		}
		return nil
	}

	ok, err := h.authSvc.VerifyUserPassword(r.Context(), userID, req.Password)
	if err != nil {
		logging.Error(packageName, "verifyStepUp: password check failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return err
	}
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That password is incorrect."})
		return errors.New("bad password")
	}
	totpOn, err := h.authSvc.IsTOTPEnabled(r.Context(), userID)
	if err != nil {
		logging.Error(packageName, "verifyStepUp: totp check failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return err
	}
	if totpOn {
		if err := h.twofa.VerifyCode(r.Context(), userID, req.Code); err != nil {
			writeStepUpErr(w, err)
			return err
		}
	}
	return nil
}

func writeStepUpErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, twofactor.ErrInvalidCode):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That code is incorrect or expired. Try the latest one from your app."})
	case errors.Is(err, passkeys.ErrInvalidAssertion), errors.Is(err, passkeys.ErrInvalidState):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "We couldn't verify your passkey. Try again."})
	case errors.Is(err, twofactor.ErrNotEnabled), errors.Is(err, passkeys.ErrNotFound):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Verification is no longer available — refresh and try again."})
	default:
		logging.Error(packageName, "step-up error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logging.Error(packageName, "writeJSON encode failed: %v", err)
	}
}
