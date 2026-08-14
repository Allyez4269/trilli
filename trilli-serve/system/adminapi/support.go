package adminapi

// support.go implements the §6.8 support-desk write: an operator reply (emailed
// to the customer as "Trilli Support") or an internal note (operator-only, no
// email, no status change). Ticket reads are done by CMX directly (Option C);
// only the side-effecting write goes through here. The acting operator is in the
// X-CMX-Operator-* headers validated by requirePrincipal.

import (
	"encoding/json"
	"errors"
	"net/http"

	"trilli/system/logging"
	"trilli/system/supporttickets"
)

// ticketReply posts an agent reply or internal note to a ticket.
// Body: {body, status, internal}. internal=true → operator-only note.
func (h *Handlers) ticketReply(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.support == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "support not configured"})
		return
	}
	var req struct {
		Body     string `json:"body"`
		Status   string `json:"status"`
		Internal bool   `json:"internal"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	var err error
	if req.Internal {
		// Attribute the note to the operator (only operators ever see it).
		err = h.support.PostInternalNote(r.Context(), id, r.Header.Get(headerOperator), req.Body)
	} else {
		// Customer-visible reply is always branded "Trilli Support".
		err = h.support.PostAgentMessage(r.Context(), id, "Trilli Support", req.Body, req.Status)
	}
	if errors.Is(err, supporttickets.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "ticket not found"})
		return
	}
	if errors.Is(err, supporttickets.ErrValidation) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a message body is required"})
		return
	}
	if err != nil {
		logging.Error(packageName, "ticketReply ticket=%d: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not post the reply"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
