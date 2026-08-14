package support

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
)

// Handler exposes the Support desk API (SPEC §6.8). Reads (cross-tenant triage
// + thread) are available to any authenticated operator; replies / internal
// notes are WRITES proxied to the app and require a fresh step-up 2FA, recorded
// in the CMX audit.
type Handler struct {
	svc *Service
	mw  *operators.Handler
	ops *operators.Service
	app *appadmin.Client
}

// NewHandler builds the support Handler.
func NewHandler(svc *Service, mw *operators.Handler, ops *operators.Service, app *appadmin.Client) *Handler {
	return &Handler{svc: svc, mw: mw, ops: ops, app: app}
}

// Register mounts the support routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmx/support/tickets", h.mw.RequireAuth(h.listTickets))
	mux.HandleFunc("GET /api/cmx/support/tickets/{id}", h.mw.RequireAuth(h.getTicket))
	mux.HandleFunc("POST /api/cmx/support/tickets/{id}/reply",
		h.mw.RequireAuth(h.mw.RequireStepUp(h.reply)))
}

func (h *Handler) listTickets(w http.ResponseWriter, r *http.Request) {
	tickets, err := h.svc.ListTickets(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("q"))
	if err != nil {
		logging.Error(packageName, "list tickets: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func (h *Handler) getTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := h.svc.GetTicket(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if err != nil {
		logging.Error(packageName, "get ticket: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) reply(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
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
	op := operators.OperatorFrom(r.Context())
	lc := operators.LoginContext{}
	if sess := operators.SessionFrom(r.Context()); sess != nil {
		lc = operators.LoginContext{IP: sess.IP, Geo: operators.Geo{ContinentCode: sess.ContinentCode, CountryCode: sess.CountryCode, Region: sess.Region}}
	}
	act := appadmin.ActingOperator{ID: op.ID, Email: op.Email}
	if err := h.app.TicketReply(r.Context(), id, req.Body, req.Status, req.Internal, act); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	action, summary := "support.reply", "Replied to ticket "+strconv.FormatInt(id, 10)
	if req.Internal {
		action, summary = "support.note", "Posted internal note on ticket "+strconv.FormatInt(id, 10)
	}
	_ = h.ops.Audit(r.Context(), op, lc, operators.AuditInput{
		Action: action, TargetType: "ticket", TargetID: strconv.FormatInt(id, 10),
		Summary: summary, Meta: map[string]any{"status": req.Status, "internal": req.Internal},
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
