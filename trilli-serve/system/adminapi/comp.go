package adminapi

import (
	"errors"
	"net/http"
	"strconv"

	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/signupintents"
)

// compAcceptBase is the registration page an invitee lands on — the SAME page
// paid sign-ups use after payment. A comp invite is a signup_intent flagged
// is_comp, so /signup/complete renders the unified registration form.
const compAcceptBase = "https://app.trilli.com/signup/complete/"

func (h *Handlers) createCompInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email              string `json:"email"`
		PlanCode           string `json:"plan_code"`
		FreeTermDays       int    `json:"free_term_days"`
		InviteValidForDays int    `json:"invite_valid_for_days"`
	}
	if !decode(w, r, &req) {
		return
	}
	opID, _ := strconv.ParseInt(r.Header.Get(headerOpID), 10, 64)
	inv, err := h.signup.CreateComp(r.Context(), req.Email, req.PlanCode,
		req.FreeTermDays, req.InviteValidForDays, opID, r.Header.Get(headerOperator))
	if errors.Is(err, signupintents.ErrEmailRegistered) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "An account with this email already exists."})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	// Email the acceptance link (best-effort — the invite stands regardless; the
	// operator can copy the link from CMX).
	if h.mailer != nil {
		planName := h.comp.PlanName(r.Context(), inv.PlanCode)
		if mErr := h.mailer.SendCompInvite(r.Context(), mailer.CompInviteEmail{
			To:           inv.Email,
			PlanName:     planName,
			FreeTermDays: inv.CompFreeTermDays,
			Link:         compAcceptBase + inv.Token,
			ExpiresAt:    inv.ExpiresAt,
		}); mErr != nil {
			logging.Error(packageName, "comp invite email failed for %s: %v", inv.Email, mErr)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": inv.ID, "token": inv.Token})
}

func (h *Handlers) revokeCompInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := h.signup.Cancel(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "invite not found or not revocable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handlers) deleteCompInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := h.signup.DeleteComp(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "invite not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
