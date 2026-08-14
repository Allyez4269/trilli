package files

import (
	"encoding/json"
	"net/http"
	"strconv"

	"trilli/system/auth"
	"trilli/system/httpx"
)

// UploadInit (POST /api/files/upload/init) reserves a resumable upload session
// and returns the chunking plan. Access is checked up front, like Upload.
func (h *Handlers) UploadInit(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var body struct {
		Name        string `json:"name"`
		ContentType string `json:"content_type"`
		TotalSize   int64  `json:"total_size_bytes"`
		FolderID    *int64 `json:"folder_id"`
		WorkspaceID *int64 `json:"workspace_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request body"})
		return
	}
	if !id.Access.CanWriteAt(body.FolderID, body.WorkspaceID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(id)})
		return
	}
	sess, err := h.svc.InitUpload(r.Context(), InitUploadInput{
		TenantID:       id.Tenant.ID,
		UserID:         id.User.ID,
		Name:           body.Name,
		ContentType:    body.ContentType,
		TotalSize:      body.TotalSize,
		ParentFolderID: body.FolderID,
		WorkspaceID:    body.WorkspaceID,
	})
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// UploadStatus (GET /api/files/upload/{token}) reports which chunks have landed,
// so a resuming client re-sends only the missing ones.
func (h *Handlers) UploadStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	sess, err := h.svc.UploadStatus(r.Context(), id.Tenant.ID, r.PathValue("token"))
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// UploadChunk (PUT /api/files/upload/{token}/chunk/{index}) streams one chunk's
// raw bytes straight into the encrypt+stage path. Idempotent per index.
func (h *Handlers) UploadChunk(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid chunk index"})
		return
	}
	// Inactivity-based deadline on the chunk body (same as Upload), so the blanket
	// server ReadTimeout can't truncate a steadily-arriving chunk.
	r.Body = httpx.NewIdleReader(w, r.Body, httpx.DefaultTransferIdleTimeout)
	if err := h.svc.StageUploadChunk(r.Context(), id.Tenant.ID, r.PathValue("token"), index, r.Body); err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UploadComplete (POST /api/files/upload/{token}/complete) commits the staged
// blocks into the final file once every chunk has landed.
func (h *Handlers) UploadComplete(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	f, err := h.svc.CompleteUpload(r.Context(), id.Tenant.ID, id.User.ID, r.PathValue("token"))
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

// UploadAbort (DELETE /api/files/upload/{token}) discards a session and its
// staged blocks.
func (h *Handlers) UploadAbort(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	if err := h.svc.AbortUpload(r.Context(), id.Tenant.ID, r.PathValue("token")); err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
