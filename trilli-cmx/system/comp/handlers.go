package comp

import (
	"encoding/json"
	"net/http"
	"strconv"

	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
)

// Handler exposes the CMX comp/ambassador API (SPEC §6.10). Both CMX and Global
// admins may send invites (CMX admins bound by the Global-set guardrail); plan
// eligibility/term policy is Global-only to edit.
type Handler struct {
	svc *Service
	mw  *operators.Handler
	ops *operators.Service
	app *appadmin.Client
}

// NewHandler builds the CMX comp Handler.
func NewHandler(svc *Service, mw *operators.Handler, ops *operators.Service, app *appadmin.Client) *Handler {
	return &Handler{svc: svc, mw: mw, ops: ops, app: app}
}

// Register mounts the comp routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmx/comp-invites", h.mw.RequireAuth(h.list))
	mux.HandleFunc("POST /api/cmx/comp-invites", h.mw.RequireAuth(h.mw.RequireStepUp(h.create)))
	// Revoke + delete are low-risk registry actions — no step-up required.
	mux.HandleFunc("POST /api/cmx/comp-invites/{id}/revoke", h.mw.RequireAuth(h.revoke))
	mux.HandleFunc("POST /api/cmx/comp-invites/{id}/delete", h.mw.RequireAuth(h.delete))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	op := operators.OperatorFrom(r.Context())
	// Global admins see every operator's invites; CMX admins see only their own.
	var onlyOp int64
	if !op.IsGlobal() {
		onlyOp = op.ID
	}
	rows, err := h.svc.ListInvites(r.Context(), r.URL.Query().Get("status"), limit, onlyOp)
	if err != nil {
		logging.Error(packageName, "list invites: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": rows})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email              string `json:"email"`
		PlanCode           string `json:"plan_code"`
		FreeTermDays       int    `json:"free_term_days"`
		InviteValidForDays int    `json:"invite_valid_for_days"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	op := operators.OperatorFrom(r.Context())
	lc := lcFrom(r)
	id, err := h.app.CreateCompInvite(r.Context(), map[string]any{
		"email":                 req.Email,
		"plan_code":             req.PlanCode,
		"free_term_days":        req.FreeTermDays,
		"invite_valid_for_days": req.InviteValidForDays,
	}, appadmin.ActingOperator{ID: op.ID, Email: op.Email})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	_ = h.ops.Audit(r.Context(), op, lc, operators.AuditInput{
		Action: "comp.invite", TargetType: "comp_invite", TargetID: strconv.FormatInt(id, 10),
		Summary: "Sent comp invite to " + req.Email + " (plan " + req.PlanCode + ")",
		Meta:    map[string]any{"email": req.Email, "plan": req.PlanCode, "term_days": req.FreeTermDays},
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	op := operators.OperatorFrom(r.Context())
	if aerr := h.app.RevokeCompInvite(r.Context(), id, appadmin.ActingOperator{ID: op.ID, Email: op.Email}); aerr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": aerr.Error()})
		return
	}
	_ = h.ops.Audit(r.Context(), op, lcFrom(r), operators.AuditInput{
		Action: "comp.revoke", TargetType: "comp_invite", TargetID: strconv.FormatInt(id, 10),
		Summary: "Revoked comp invite " + strconv.FormatInt(id, 10),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	op := operators.OperatorFrom(r.Context())
	if aerr := h.app.DeleteCompInvite(r.Context(), id, appadmin.ActingOperator{ID: op.ID, Email: op.Email}); aerr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": aerr.Error()})
		return
	}
	_ = h.ops.Audit(r.Context(), op, lcFrom(r), operators.AuditInput{
		Action: "comp.delete", TargetType: "comp_invite", TargetID: strconv.FormatInt(id, 10),
		Summary: "Deleted comp invite " + strconv.FormatInt(id, 10),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func lcFrom(r *http.Request) operators.LoginContext {
	if sess := operators.SessionFrom(r.Context()); sess != nil {
		return operators.LoginContext{
			IP:  sess.IP,
			Geo: operators.Geo{ContinentCode: sess.ContinentCode, CountryCode: sess.CountryCode, Region: sess.Region},
		}
	}
	return operators.LoginContext{}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
