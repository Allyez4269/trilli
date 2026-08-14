package workspaces

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"trilli/system/auth"
	"trilli/system/logging"
)

// Handlers exposes the workspace HTTP surface.
type Handlers struct {
	svc *Service
}

// NewHandlers wraps a Service.
func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the workspace routes. Listing is available to any member (so
// the My Files picker can show their workspaces); create/assign are owner/admin.
func (h *Handlers) Register(m muxLike, requireAuth, requireAdminOrOwner func(http.Handler) http.Handler) {
	m.Handle("GET /api/workspaces", requireAuth(http.HandlerFunc(h.List)))
	m.Handle("POST /api/workspaces", requireAdminOrOwner(http.HandlerFunc(h.Create)))
	m.Handle("PATCH /api/workspaces/{id}", requireAdminOrOwner(http.HandlerFunc(h.Update)))
	m.Handle("GET /api/workspaces/{id}/members", requireAdminOrOwner(http.HandlerFunc(h.ListMembers)))
	m.Handle("POST /api/workspaces/{id}/members", requireAdminOrOwner(http.HandlerFunc(h.Assign)))
	m.Handle("DELETE /api/workspaces/{id}/members/{userId}", requireAdminOrOwner(http.HandlerFunc(h.Unassign)))
	// User-centric assignment: read/replace a member's whole set of workspaces.
	m.Handle("GET /api/members/{id}/workspaces", requireAdminOrOwner(http.HandlerFunc(h.GetMemberWorkspaces)))
	m.Handle("PATCH /api/members/{id}/workspaces", requireAdminOrOwner(http.HandlerFunc(h.SetMemberWorkspaces)))
	// Workspace-scoped access editor (Edit Workspace modal): roster + per-member set.
	m.Handle("GET /api/workspaces/{id}/access", requireAdminOrOwner(http.HandlerFunc(h.AccessRoster)))
	m.Handle("PATCH /api/workspaces/{id}/members/{userId}/access", requireAdminOrOwner(http.HandlerFunc(h.SetMemberAccess)))
}

// AccessRoster handles GET /api/workspaces/{id}/access.
func (h *Handlers) AccessRoster(w http.ResponseWriter, r *http.Request) {
	identity, id, ok := h.identityAndID(w, r)
	if !ok {
		return
	}
	roster, err := h.svc.AccessRoster(r.Context(), identity.Tenant.ID, id)
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": roster})
}

// SetMemberAccess handles PUT /api/workspaces/{id}/members/{userId}/access.
// Body: {"whole": bool, "folder_ids": [N, ...]}.
func (h *Handlers) SetMemberAccess(w http.ResponseWriter, r *http.Request) {
	identity, id, ok := h.identityAndID(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	var req struct {
		Whole     bool    `json:"whole"`
		FolderIDs []int64 `json:"folder_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := h.svc.SetMemberAccess(r.Context(), identity.Tenant.ID, id, userID, req.Whole, req.FolderIDs, identity.User.ID); err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetMemberWorkspaces handles GET /api/members/{id}/workspaces. The path id is
// the target user's id. Returns {"workspace_ids": [...]}.
func (h *Handlers) GetMemberWorkspaces(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	ids, err := h.svc.ListMemberWorkspaceIDs(r.Context(), identity.Tenant.ID, userID)
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace_ids": ids})
}

// SetMemberWorkspaces handles PATCH /api/members/{id}/workspaces. The path id is
// the target user's id. Body: {"workspace_ids": [N, ...]}.
func (h *Handlers) SetMemberWorkspaces(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	var req struct {
		WorkspaceIDs []int64 `json:"workspace_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := h.svc.SetMemberWorkspaces(r.Context(), identity.Tenant.ID, userID, req.WorkspaceIDs, identity.User.ID); err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// List returns the caller's accessible workspaces plus the account storage pool
// (for owner/admin, who allocate from it when creating new workspaces).
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	list, err := h.svc.ListForIdentity(r.Context(), identity)
	if err != nil {
		logging.Error(packageName, "List failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	resp := map[string]any{"workspaces": list}
	if auth.IsAdminOrOwner(identity.Membership.Role) {
		if q, qErr := h.svc.AccountQuotaFor(r.Context(), identity.Tenant.ID); qErr == nil {
			resp["account"] = q
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// Create handles POST /api/workspaces. Body: {"name": "...", "disk_allocation_bytes": N}.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Name                string `json:"name"`
		DiskAllocationBytes int64  `json:"disk_allocation_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	ws, err := h.svc.Create(r.Context(), identity.Tenant.ID, identity.User.ID, req.Name, req.DiskAllocationBytes)
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

// Update handles PATCH /api/workspaces/{id}. Body: {"name"?, "disk_allocation_bytes"?}.
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	identity, id, ok := h.identityAndID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name                *string `json:"name"`
		DiskAllocationBytes *int64  `json:"disk_allocation_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	ws, err := h.svc.Update(r.Context(), identity.Tenant.ID, id, UpdateInput{
		Name: req.Name, AllocBytes: req.DiskAllocationBytes,
	})
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

// ListMembers handles GET /api/workspaces/{id}/members.
func (h *Handlers) ListMembers(w http.ResponseWriter, r *http.Request) {
	identity, id, ok := h.identityAndID(w, r)
	if !ok {
		return
	}
	members, err := h.svc.ListMembers(r.Context(), identity.Tenant.ID, id)
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// Assign handles POST /api/workspaces/{id}/members. Body: {"user_id": N}.
func (h *Handlers) Assign(w http.ResponseWriter, r *http.Request) {
	identity, id, ok := h.identityAndID(w, r)
	if !ok {
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := h.svc.Assign(r.Context(), identity.Tenant.ID, id, req.UserID, identity.User.ID); err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unassign handles DELETE /api/workspaces/{id}/members/{userId}.
func (h *Handlers) Unassign(w http.ResponseWriter, r *http.Request) {
	identity, id, ok := h.identityAndID(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	if err := h.svc.Unassign(r.Context(), identity.Tenant.ID, id, userID); err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// identityAndID resolves the authed identity + the {id} path value, writing the
// appropriate error response and returning ok=false on failure.
func (h *Handlers) identityAndID(w http.ResponseWriter, r *http.Request) (*auth.Identity, int64, bool) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return nil, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return nil, 0, false
	}
	return identity, id, true
}

func writeWorkspaceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
	case errors.Is(err, ErrNameRequired), errors.Is(err, ErrAllocInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrNameTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "A workspace with that name already exists."})
	case errors.Is(err, ErrAllocExceedsPool):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "That allocation exceeds the account's available storage."})
	case errors.Is(err, ErrWorkspaceLimit):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "You've reached your plan's workspace limit. Upgrade to add more."})
	case errors.Is(err, ErrAllocBelowUsage):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Allocation can't be lower than the storage already used."})
	case errors.Is(err, ErrNotTenantMember):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That user isn't a member of this account."})
	default:
		logging.Error(packageName, "handler error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logging.Error(packageName, "writeJSON encode failed: %v", err)
	}
}
