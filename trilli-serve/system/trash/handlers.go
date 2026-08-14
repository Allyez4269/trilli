package trash

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"trilli/system/audit"
	"trilli/system/auth"
	"trilli/system/logging"
)

// Handlers exposes the trash HTTP surface.
type Handlers struct {
	svc   *Service
	audit *audit.Service
}

func NewHandlers(svc *Service, auditSvc *audit.Service) *Handlers {
	return &Handlers{svc: svc, audit: auditSvc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the trash routes. All are tenant-scoped (RequireAuth); the
// listing + actions are further folder-access-scoped per request so a member
// only sees/acts on items from folders they can reach.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("GET /api/trash", requireAuth(http.HandlerFunc(h.List)))
	m.Handle("GET /api/trash/count", requireAuth(http.HandlerFunc(h.Count)))
	m.Handle("POST /api/trash/{batch}/restore", requireAuth(http.HandlerFunc(h.Restore)))
	m.Handle("DELETE /api/trash/{batch}", requireAuth(http.HandlerFunc(h.Purge)))
}

type errorResp struct {
	Error string `json:"error"`
}

// scopeFor builds a trash Scope from the identity's folder access — nil for
// owner/admin (see everything), the accessible folder set for a scoped member.
func scopeFor(identity *auth.Identity) *Scope {
	if identity.Access == nil || !identity.Access.Restricted() {
		return nil
	}
	return &Scope{IDs: identity.Access.FolderIDs(), WorkspaceIDs: identity.Access.WorkspaceIDs()}
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	entries, err := h.svc.ListRoots(r.Context(), identity.Tenant.ID, scopeFor(identity))
	if err != nil {
		logging.Error(packageName, "List failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": entries})
}

func (h *Handlers) Count(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	n, err := h.svc.CountRoots(r.Context(), identity.Tenant.ID, scopeFor(identity))
	if err != nil {
		logging.Error(packageName, "Count failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (h *Handlers) Restore(w http.ResponseWriter, r *http.Request) {
	identity, batch, ok := h.gate(w, r)
	if !ok {
		return
	}
	restored, err := h.svc.Restore(r.Context(), identity.Tenant.ID, batch)
	if err != nil {
		h.writeActionErr(w, err)
		return
	}
	// Audit the restore to its (resolved) location.
	targetID := restored.ID
	tt := audit.TargetFile
	if restored.Kind == KindFolder {
		tt = audit.TargetFolder
	}
	h.audit.Record(r.Context(), audit.RecordInput{
		TenantID: identity.Tenant.ID, ActorUserID: identity.User.ID, ActorEmail: identity.User.Email,
		Action: audit.ActionRestore, TargetType: tt, TargetID: &targetID,
		ItemName: restored.Name,
		ItemPath: h.audit.PathOf(r.Context(), identity.Tenant.ID, restored.ParentFolderID),
		Device:   audit.DeviceFromUA(r.UserAgent()), IP: auth.ClientIP(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"restored": map[string]any{"kind": restored.Kind, "id": restored.ID, "name": restored.Name},
	})
}

func (h *Handlers) Purge(w http.ResponseWriter, r *http.Request) {
	identity, batch, ok := h.gate(w, r)
	if !ok {
		return
	}
	if err := h.svc.PurgeBatch(r.Context(), identity.Tenant.ID, batch); err != nil {
		h.writeActionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// gate resolves the batch id, loads its root, and enforces write access to
// the item's original location — the same bar that deleting it required.
func (h *Handlers) gate(w http.ResponseWriter, r *http.Request) (*auth.Identity, int64, bool) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return nil, 0, false
	}
	batch, err := strconv.ParseInt(r.PathValue("batch"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return nil, 0, false
	}
	root, err := h.svc.BatchRoot(r.Context(), identity.Tenant.ID, batch)
	if err != nil {
		h.writeActionErr(w, err)
		return nil, 0, false
	}
	if !identity.Access.CanWriteAt(root.ParentFolderID, &root.WorkspaceID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: "You don't have access to this item's location"})
		return nil, 0, false
	}
	return identity, batch, true
}

func (h *Handlers) writeActionErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrBatchNotFound) {
		writeJSON(w, http.StatusNotFound, errorResp{Error: err.Error()})
		return
	}
	logging.Error(packageName, "trash action failed: %v", err)
	writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logging.Error(packageName, "writeJSON encode failed: %v", err)
	}
}
