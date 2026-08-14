package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"trilli/system/plans"
)

func (h *Handlers) createPlan(w http.ResponseWriter, r *http.Request) {
	var in plans.Input
	if !decode(w, r, &in) {
		return
	}
	id, err := h.plans.Create(r.Context(), in)
	if errors.Is(err, plans.ErrDuplicateCode) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "a plan with that code already exists"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (h *Handlers) updatePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in plans.Input
	if !decode(w, r, &in) {
		return
	}
	err := h.plans.Update(r.Context(), id, in)
	if errors.Is(err, plans.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "plan not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handlers) setPlanStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &req) {
		return
	}
	// Reassign-before-archive guard: don't strand active subscribers (SPEC §6.3).
	if req.Status == "archived" {
		n, cerr := h.plans.CountActiveAccounts(r.Context(), id)
		if cerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
			return
		}
		if n > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "plan has active subscribers; reassign them before archiving",
			})
			return
		}
	}
	err := h.plans.SetStatus(r.Context(), id, req.Status)
	if errors.Is(err, plans.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "plan not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": req.Status})
}

// decode reads a JSON body (size-capped), writing a 400 on failure.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return false
	}
	return true
}
