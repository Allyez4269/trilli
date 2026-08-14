package admin

// operators.go implements Administration operator management (SPEC §6.7):
// list/create/role/status/unlock/reset-password (Global + step-up) plus a
// self-service change-password (any operator + step-up). Every mutation is
// recorded in the operator audit. The ≥1-active-Global invariant is enforced in
// the operators store.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"trilli-cmx/system/operators"
)

// registerOperators mounts the operator-management routes (called from Register).
func (h *Handler) registerOperators(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmx/admin/operators", h.mw.RequireGlobal(h.listOperators))
	mux.HandleFunc("POST /api/cmx/admin/operators",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.createOperator)))
	mux.HandleFunc("POST /api/cmx/admin/operators/{id}/role",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.setOperatorRole)))
	mux.HandleFunc("POST /api/cmx/admin/operators/{id}/status",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.setOperatorStatus)))
	mux.HandleFunc("POST /api/cmx/admin/operators/{id}/unlock",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.unlockOperator)))
	mux.HandleFunc("POST /api/cmx/admin/operators/{id}/password",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.resetOperatorPassword)))
	// Self change-password — any authenticated operator (e.g. rotate bootstrap pw).
	mux.HandleFunc("POST /api/cmx/admin/password",
		h.mw.RequireAuth(h.mw.RequireStepUp(h.changeOwnPassword)))
	// Self profile (name + email) — any authenticated operator, no step-up.
	mux.HandleFunc("POST /api/cmx/admin/profile", h.mw.RequireAuth(h.changeOwnProfile))
}

func (h *Handler) changeOwnProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if !decode(w, r, &req) {
		return
	}
	self := operators.OperatorFrom(r.Context())
	if err := h.ops.SetProfile(r.Context(), self.ID, req.Name, req.Email); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, "operator.profile", "Updated own profile", self.ID, map[string]any{"email": req.Email})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) audit(r *http.Request, action, summary string, targetID int64, meta map[string]any) {
	op := operators.OperatorFrom(r.Context())
	lc := operators.LoginContext{}
	if sess := operators.SessionFrom(r.Context()); sess != nil {
		lc = operators.LoginContext{IP: sess.IP, Geo: operators.Geo{ContinentCode: sess.ContinentCode, CountryCode: sess.CountryCode, Region: sess.Region}}
	}
	_ = h.ops.Audit(r.Context(), op, lc, operators.AuditInput{
		Action: action, TargetType: "operator", TargetID: strconv.FormatInt(targetID, 10),
		Summary: summary, Meta: meta,
	})
}

func (h *Handler) listOperators(w http.ResponseWriter, r *http.Request) {
	ops, err := h.ops.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
		return
	}
	if ops == nil {
		ops = []*operators.Operator{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"operators": ops})
}

func (h *Handler) createOperator(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	actor := operators.OperatorFrom(r.Context())
	op, err := h.ops.Create(r.Context(), operators.CreateInput{
		Email: req.Email, Name: req.Name, Password: req.Password,
		Role: operators.Role(req.Role), CreatedBy: &actor.ID,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, "operator.create", "Created operator "+op.Email+" ("+string(op.Role)+")", op.ID,
		map[string]any{"email": op.Email, "role": string(op.Role)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "operator": op})
}

func (h *Handler) setOperatorRole(w http.ResponseWriter, r *http.Request) {
	id, ok := adminPathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := h.ops.SetRole(r.Context(), id, operators.Role(req.Role)); err != nil {
		h.mutErr(w, err)
		return
	}
	h.audit(r, "operator.role", "Set operator "+strconv.FormatInt(id, 10)+" role → "+req.Role, id, map[string]any{"role": req.Role})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) setOperatorStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := adminPathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := h.ops.SetStatus(r.Context(), id, operators.Status(req.Status)); err != nil {
		h.mutErr(w, err)
		return
	}
	h.audit(r, "operator.status", "Set operator "+strconv.FormatInt(id, 10)+" status → "+req.Status, id, map[string]any{"status": req.Status})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) unlockOperator(w http.ResponseWriter, r *http.Request) {
	id, ok := adminPathID(w, r)
	if !ok {
		return
	}
	if err := h.ops.Unlock(r.Context(), id); err != nil {
		h.mutErr(w, err)
		return
	}
	h.audit(r, "operator.unlock", "Unlocked operator "+strconv.FormatInt(id, 10), id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) resetOperatorPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := adminPathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := h.ops.SetPassword(r.Context(), id, req.Password); err != nil {
		h.mutErr(w, err)
		return
	}
	h.audit(r, "operator.password_reset", "Reset password for operator "+strconv.FormatInt(id, 10), id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	self := operators.OperatorFrom(r.Context())
	if err := h.ops.SetPassword(r.Context(), self.ID, req.Password); err != nil {
		h.mutErr(w, err)
		return
	}
	h.audit(r, "operator.password_self", "Changed own password", self.ID, nil)
	// The store revoked all sessions; the client must re-login.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reauth": true})
}

func (h *Handler) mutErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, operators.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "operator not found"})
	case errors.Is(err, operators.ErrLastGlobal):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
}

func adminPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return false
	}
	return true
}
