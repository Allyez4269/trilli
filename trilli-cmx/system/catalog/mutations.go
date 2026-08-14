package catalog

import (
	"encoding/json"
	"net/http"
	"strconv"

	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/operators"
)

// actor extracts the acting operator + request geo for the app call + audit.
func actor(r *http.Request) (*operators.Operator, operators.LoginContext, appadmin.ActingOperator) {
	op := operators.OperatorFrom(r.Context())
	lc := operators.LoginContext{}
	if sess := operators.SessionFrom(r.Context()); sess != nil {
		lc = operators.LoginContext{
			IP:  sess.IP,
			Geo: operators.Geo{ContinentCode: sess.ContinentCode, CountryCode: sess.CountryCode, Region: sess.Region},
		}
	}
	return op, lc, appadmin.ActingOperator{ID: op.ID, Email: op.Email}
}

func (h *Handler) audit(r *http.Request, op *operators.Operator, lc operators.LoginContext, action, summary, planID string, meta map[string]any) {
	_ = h.ops.Audit(r.Context(), op, lc, operators.AuditInput{
		Action: action, TargetType: "plan", TargetID: planID, Summary: summary, Meta: meta,
	})
}

func (h *Handler) createPlan(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if !decodeJSON(w, r, &in) {
		return
	}
	op, lc, act := actor(r)
	id, err := h.app.CreatePlan(r.Context(), in, act)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	code, _ := in["code"].(string)
	h.audit(r, op, lc, "plan.create", "Created plan "+code, strconv.FormatInt(id, 10), map[string]any{"code": code})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (h *Handler) updatePlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var in map[string]any
	if !decodeJSON(w, r, &in) {
		return
	}
	op, lc, act := actor(r)
	if err := h.app.UpdatePlan(r.Context(), id, in, act); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, op, lc, "plan.update", "Edited plan "+strconv.FormatInt(id, 10), strconv.FormatInt(id, 10), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) setPlanStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	op, lc, act := actor(r)
	if err := h.app.SetPlanStatus(r.Context(), id, req.Status, act); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, op, lc, "plan.status", "Set plan "+strconv.FormatInt(id, 10)+" -> "+req.Status,
		strconv.FormatInt(id, 10), map[string]any{"status": req.Status})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": req.Status})
}

// decodeJSON decodes a size-capped JSON body, writing 400 on error.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return false
	}
	return true
}
