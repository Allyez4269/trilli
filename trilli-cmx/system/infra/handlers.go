package infra

import (
	"encoding/json"
	"net/http"
	"strconv"

	"trilli-cmx/system/appadmin"
	"trilli-cmx/system/logging"
	"trilli-cmx/system/operators"
)

// Handler exposes the Infrastructure API (SPEC §6.5). Global-only. Reads (jobs/
// health/cost) are pure; the WRITES (run-now a job, on-demand tiering dry-run/
// apply) are proxied to the app's admin surface, step-up gated, and audited.
type Handler struct {
	svc *Service
	mw  *operators.Handler
	ops *operators.Service
	app *appadmin.Client
}

// NewHandler builds the infra Handler.
func NewHandler(svc *Service, mw *operators.Handler, ops *operators.Service, app *appadmin.Client) *Handler {
	return &Handler{svc: svc, mw: mw, ops: ops, app: app}
}

// Register mounts the infra routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cmx/infra/jobs", h.mw.RequireGlobal(h.jobs))
	mux.HandleFunc("GET /api/cmx/infra/health", h.mw.RequireGlobal(h.health))
	mux.HandleFunc("GET /api/cmx/infra/cost", h.mw.RequireGlobal(h.cost))

	// Background-job run-now — Global + fresh step-up, proxied + audited.
	// (Storage tiering runs automatically via the scheduled engine — no manual
	// trigger; the dashboard shows its status only.)
	mux.HandleFunc("POST /api/cmx/infra/jobs/{job}/run",
		h.mw.RequireGlobal(h.mw.RequireStepUp(h.runJob)))
}

func (h *Handler) auditInfra(r *http.Request, action, summary string, meta map[string]any) {
	op := operators.OperatorFrom(r.Context())
	lc := operators.LoginContext{}
	if sess := operators.SessionFrom(r.Context()); sess != nil {
		lc = operators.LoginContext{IP: sess.IP, Geo: operators.Geo{ContinentCode: sess.ContinentCode, CountryCode: sess.CountryCode, Region: sess.Region}}
	}
	_ = h.ops.Audit(r.Context(), op, lc, operators.AuditInput{Action: action, TargetType: "infra", Summary: summary, Meta: meta})
}

func (h *Handler) actor(r *http.Request) appadmin.ActingOperator {
	op := operators.OperatorFrom(r.Context())
	return appadmin.ActingOperator{ID: op.ID, Email: op.Email}
}

func (h *Handler) runJob(w http.ResponseWriter, r *http.Request) {
	job := r.PathValue("job")
	res, err := h.app.RunJob(r.Context(), job, h.actor(r))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	h.auditInfra(r, "infra.job_run", "Ran job "+job+" (ran="+strconv.FormatBool(res.Ran)+")", map[string]any{"job": job, "ran": res.Ran, "node": res.Node})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ran": res.Ran, "node": res.Node, "job": res.Job})
}

func (h *Handler) jobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListJobs(r.Context())
	if err != nil {
		h.fail(w, "jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	hh, err := h.svc.Health(r.Context())
	if err != nil {
		h.fail(w, "health", err)
		return
	}
	writeJSON(w, http.StatusOK, hh)
}

func (h *Handler) cost(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Cost(r.Context())
	if err != nil {
		h.fail(w, "cost", err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) fail(w http.ResponseWriter, what string, err error) {
	logging.Error(packageName, "%s: %v", what, err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Something went wrong."})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
