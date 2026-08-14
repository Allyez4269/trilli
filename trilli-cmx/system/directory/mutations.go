package directory

import (
	"net/http"
	"strconv"

	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/operators"
)

// actor extracts the acting operator + request geo from the authenticated
// request, for both the app-admin call and the operator audit.
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

// audit records an operator action (best-effort) for a tenant mutation.
func (h *Handler) audit(r *http.Request, op *operators.Operator, lc operators.LoginContext, action, summary string, tenantID int64, meta map[string]any) {
	_ = h.ops.Audit(r.Context(), op, lc, operators.AuditInput{
		Action:     action,
		TargetType: "tenant",
		TargetID:   strconv.FormatInt(tenantID, 10),
		TenantID:   &tenantID,
		Summary:    summary,
		Meta:       meta,
	})
}

func (h *Handler) suspendTenant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	op, lc, act := actor(r)
	if err := h.app.SuspendTenant(r.Context(), id, act); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, op, lc, "tenant.suspend", "Suspended tenant "+strconv.FormatInt(id, 10), id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "suspended"})
}

func (h *Handler) reactivateTenant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	op, lc, act := actor(r)
	if err := h.app.ReactivateTenant(r.Context(), id, act); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, op, lc, "tenant.reactivate", "Reactivated tenant "+strconv.FormatInt(id, 10), id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "active"})
}

func (h *Handler) quotaOverride(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Bytes *int64 `json:"bytes"` // null clears the override
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	op, lc, act := actor(r)
	if err := h.app.SetQuotaOverride(r.Context(), id, req.Bytes, act); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	summary := "Cleared quota override on tenant " + strconv.FormatInt(id, 10)
	meta := map[string]any{"bytes": nil}
	if req.Bytes != nil {
		summary = "Set quota override on tenant " + strconv.FormatInt(id, 10)
		meta = map[string]any{"bytes": *req.Bytes}
	}
	h.audit(r, op, lc, "tenant.quota_override", summary, id, meta)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "quota_override_bytes": req.Bytes})
}
