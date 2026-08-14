package adminapi

// infra.go implements the §6.5 Infrastructure write ops for Trilli CMX:
// run-now a coordinated background job (under the cluster advisory lock) and an
// on-demand storage-tiering dry-run / apply. Both go through requirePrincipal
// like every other admin route; the cluster lock ensures only one node acts.

import (
	"context"
	"encoding/json"
	"net/http"

	"trilli/system/logging"
)

// runJob triggers a named background job immediately via the cluster
// coordinator. {job} must be a registered runnable job (e.g. trash-purge,
// lifecycle-sweep, signup-sweep, blob-encrypt-migrate, diagnostic-ping).
func (h *Handlers) runJob(w http.ResponseWriter, r *http.Request) {
	if h.coord == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "jobs not configured"})
		return
	}
	job := r.PathValue("job")

	// diagnostic-ping is a built-in self-test (no registry entry needed).
	fn, ok := h.jobs[job]
	if !ok && job != "diagnostic-ping" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown job"})
		return
	}
	if fn == nil {
		fn = func(ctx context.Context) error { return nil } // diagnostic-ping: lock round-trip only
	}

	ran, err := h.coord.RunExclusive(r.Context(), job, fn)
	if err != nil {
		logging.Error(packageName, "runJob %q: %v", job, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "job failed: " + err.Error()})
		return
	}
	// ran=false means another node holds the lock (the job is already running) —
	// that's a normal outcome, not an error.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ran": ran, "node": h.coord.NodeID(), "job": job})
}

// runTiering runs one on-demand tiering pass: dry-run (plan only) by default, or
// a real apply when {"apply": true}. Returns the pass summary.
func (h *Handlers) runTiering(w http.ResponseWriter, r *http.Request) {
	if h.tierRun == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "tiering not configured"})
		return
	}
	var req struct {
		Apply bool `json:"apply"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	}
	sum, ran, err := h.tierRun(r.Context(), req.Apply)
	if err != nil {
		logging.Error(packageName, "runTiering(apply=%v): %v", req.Apply, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "tiering pass failed"})
		return
	}
	if !ran {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "a tiering pass is already running on another node"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "mode": string(sum.Mode),
		"scanned": sum.Scanned, "planned": sum.Planned, "applied": sum.Applied,
		"failed": sum.Failed, "promotions": sum.Promotions, "demotions": sum.Demotions, "capped": sum.Capped,
	})
}
