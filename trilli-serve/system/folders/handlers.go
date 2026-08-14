package folders

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"trilli/system/audit"
	"trilli/system/auth"
	"trilli/system/logging"
)

// Handlers exposes the HTTP-facing folder surface.
type Handlers struct {
	svc   *Service
	audit *audit.Service
}

func NewHandlers(svc *Service, auditSvc *audit.Service) *Handlers {
	return &Handlers{svc: svc, audit: auditSvc}
}

// record appends a folder audit event, filling in actor/device/ip from the
// request + identity. Best-effort (audit.Record never fails the caller).
func (h *Handlers) record(r *http.Request, identity *auth.Identity, in audit.RecordInput) {
	in.TenantID = identity.Tenant.ID
	in.ActorUserID = identity.User.ID
	in.ActorEmail = identity.User.Email
	in.TargetType = audit.TargetFolder
	in.Device = audit.DeviceFromUA(r.UserAgent())
	in.IP = auth.ClientIP(r)
	h.audit.Record(r.Context(), in)
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires folder routes — all RequireAuth, all tenant-scoped.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("POST /api/folders", requireAuth(http.HandlerFunc(h.Create)))
	m.Handle("GET /api/folders/starred", requireAuth(http.HandlerFunc(h.Starred)))
	m.Handle("GET /api/folders/{id}", requireAuth(http.HandlerFunc(h.Get)))
	m.Handle("PATCH /api/folders/{id}", requireAuth(http.HandlerFunc(h.Patch)))
	m.Handle("DELETE /api/folders/{id}", requireAuth(http.HandlerFunc(h.Delete)))
}

type errorResp struct {
	Error string `json:"error"`
}

type createReq struct {
	Name           string `json:"name"`
	ParentFolderID *int64 `json:"parent_folder_id"`
	WorkspaceID    *int64 `json:"workspace_id"` // optional; nil = inherit parent / account default
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	if !identity.Access.CanWriteAt(req.ParentFolderID, req.WorkspaceID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
		return
	}
	f, err := h.svc.Create(r.Context(), CreateInput{
		TenantID:        identity.Tenant.ID,
		CreatedByUserID: identity.User.ID,
		Name:            req.Name,
		ParentFolderID:  req.ParentFolderID,
		WorkspaceID:     req.WorkspaceID,
	})
	if err != nil {
		writeJSON(w, statusForFolderErr(err), errorResp{Error: err.Error()})
		return
	}
	folderID := f.ID
	h.record(r, identity, audit.RecordInput{
		Action: audit.ActionCreateFolder, TargetID: &folderID, ItemName: f.Name,
		ItemPath: h.audit.PathOf(r.Context(), identity.Tenant.ID, f.ParentFolderID),
	})
	writeJSON(w, http.StatusCreated, f)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	folderID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if !identity.Access.CanRead(&folderID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: "You don't have access to this folder"})
		return
	}
	f, err := h.svc.Get(r.Context(), identity.Tenant.ID, folderID)
	if err != nil {
		writeJSON(w, statusForFolderErr(err), errorResp{Error: err.Error()})
		return
	}
	bc, err := h.svc.Breadcrumb(r.Context(), identity.Tenant.ID, folderID)
	if err != nil {
		logging.Error(packageName, "Breadcrumb failed: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"folder": f, "breadcrumb": bc})
}

// Patch handles rename + move via one endpoint. Body fields all optional:
//
//	{"name": "x"}                  // rename
//	{"parent_folder_id": 5}        // move into folder 5
//	{"parent_folder_id": null}     // move to root
type patchReq struct {
	Name                  *string `json:"name"`
	ParentFolderID        *int64  `json:"parent_folder_id"`
	ParentFolderIDPresent bool    `json:"-"`
	WorkspaceID           *int64  `json:"workspace_id"` // target workspace for a root move (cross-workspace transfer)
	Starred               *bool   `json:"starred"`
}

func (h *Handlers) Patch(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	folderID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}

	raw := map[string]json.RawMessage{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}
	var req patchReq
	if rawName, ok := raw["name"]; ok {
		var s string
		if err := json.Unmarshal(rawName, &s); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid name"})
			return
		}
		req.Name = &s
	}
	if rawParent, ok := raw["parent_folder_id"]; ok {
		req.ParentFolderIDPresent = true
		if string(rawParent) != "null" {
			var id int64
			if err := json.Unmarshal(rawParent, &id); err != nil {
				writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid parent_folder_id"})
				return
			}
			req.ParentFolderID = &id
		}
	}
	if rawWs, ok := raw["workspace_id"]; ok && string(rawWs) != "null" {
		var id int64
		if err := json.Unmarshal(rawWs, &id); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid workspace_id"})
			return
		}
		req.WorkspaceID = &id
	}
	if rawStarred, ok := raw["starred"]; ok {
		var b bool
		if err := json.Unmarshal(rawStarred, &b); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid starred"})
			return
		}
		req.Starred = &b
	}

	// Rename, move, and star all modify this folder, so it must be writable.
	// A move additionally writes the destination parent.
	if req.Name != nil || req.ParentFolderIDPresent || req.Starred != nil {
		if !identity.Access.CanWrite(&folderID) {
			writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
			return
		}
	}
	if req.ParentFolderIDPresent && !identity.Access.CanWriteAt(req.ParentFolderID, req.WorkspaceID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
		return
	}

	// Snapshot the pre-change name + parent for the audit log (rename's old
	// name, move's source path). Best-effort — if it fails we still proceed.
	before, _ := h.svc.Get(r.Context(), identity.Tenant.ID, folderID)

	var result *Folder
	if req.Name != nil {
		f, err := h.svc.Rename(r.Context(), identity.Tenant.ID, folderID, *req.Name)
		if err != nil {
			writeJSON(w, statusForFolderErr(err), errorResp{Error: err.Error()})
			return
		}
		result = f
		if before != nil {
			prev := before.Name
			h.record(r, identity, audit.RecordInput{
				Action: audit.ActionRename, TargetID: &folderID, ItemName: f.Name, OldName: &prev,
				ItemPath: h.audit.PathOf(r.Context(), identity.Tenant.ID, f.ParentFolderID),
			})
		}
	}
	if req.ParentFolderIDPresent {
		f, err := h.svc.Move(r.Context(), identity.Tenant.ID, folderID, req.ParentFolderID, req.WorkspaceID)
		if err != nil {
			writeJSON(w, statusForFolderErr(err), errorResp{Error: err.Error()})
			return
		}
		result = f
		var fromParent *int64
		if before != nil {
			fromParent = before.ParentFolderID
		}
		from := h.audit.PathOf(r.Context(), identity.Tenant.ID, fromParent)
		to := h.audit.PathOf(r.Context(), identity.Tenant.ID, f.ParentFolderID)
		h.record(r, identity, audit.RecordInput{
			Action: audit.ActionMove, TargetID: &folderID, ItemName: f.Name,
			ItemPath: to, FromPath: &from, ToPath: &to,
		})
	}
	if req.Starred != nil {
		f, err := h.svc.SetStarred(r.Context(), identity.Tenant.ID, folderID, *req.Starred)
		if err != nil {
			writeJSON(w, statusForFolderErr(err), errorResp{Error: err.Error()})
			return
		}
		result = f
		if *req.Starred {
			h.record(r, identity, audit.RecordInput{
				Action: audit.ActionStar, TargetID: &folderID, ItemName: f.Name,
				ItemPath: h.audit.PathOf(r.Context(), identity.Tenant.ID, f.ParentFolderID),
			})
		}
	}
	if result == nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "no changes specified"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Starred returns the caller's starred folders (GET /api/folders/starred),
// scoped to what a member may access.
func (h *Handlers) Starred(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	// Owner/admin (Full) see all; scoped members are limited to their
	// accessible folder set.
	var restrict []int64
	if identity.Access != nil && !identity.Access.Full {
		restrict = make([]int64, 0, len(identity.Access.Folders))
		for id := range identity.Access.Folders {
			restrict = append(restrict, id)
		}
	}
	folders, err := h.svc.ListStarred(r.Context(), identity.Tenant.ID, restrict)
	if err != nil {
		writeJSON(w, statusForFolderErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders})
}

func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	folderID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if !identity.Access.CanWrite(&folderID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
		return
	}
	before, _ := h.svc.Get(r.Context(), identity.Tenant.ID, folderID)
	if err := h.svc.Trash(r.Context(), identity.Tenant.ID, folderID, identity.User.ID); err != nil {
		writeJSON(w, statusForFolderErr(err), errorResp{Error: err.Error()})
		return
	}
	if before != nil {
		h.record(r, identity, audit.RecordInput{
			Action: audit.ActionDelete, TargetID: &folderID, ItemName: before.Name,
			ItemPath: h.audit.PathOf(r.Context(), identity.Tenant.ID, before.ParentFolderID),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// forbiddenWriteMsg distinguishes a read-only (viewer) refusal from an
// out-of-scope folder refusal so the UI can show a useful message.
func forbiddenWriteMsg(identity *auth.Identity) string {
	if identity.Access != nil && !identity.Access.Write {
		return "Your role is read-only — you can't modify folders"
	}
	return "You don't have access to this folder"
}

// ----- helpers ------------------------------------------------------------

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

func statusForFolderErr(err error) int {
	switch {
	case errors.Is(err, ErrFolderNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrContainsProtected):
		return http.StatusForbidden
	case errors.Is(err, ErrInvalidName):
		return http.StatusBadRequest
	case errors.Is(err, ErrInvalidParent), errors.Is(err, ErrInvalidWorkspace):
		return http.StatusBadRequest
	case errors.Is(err, ErrCycle):
		return http.StatusBadRequest
	case errors.Is(err, ErrNameTaken):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
