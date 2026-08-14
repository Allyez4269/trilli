package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"trilli/system/audit"
	"trilli/system/auth"
	"trilli/system/docpreview"
	"trilli/system/folders"
	"trilli/system/httpx"
	"trilli/system/logging"
	"trilli/system/thumbs"
)

// Handlers exposes the HTTP-facing file surface.
type Handlers struct {
	svc     *Service
	audit   *audit.Service
	auth    *auth.Service    // authorizes the actor against a non-session tenant (cross-account copy)
	folders *folders.Service // walks/creates folder trees for cross-account folder copy
}

// NewHandlers constructs the HTTP handlers wrapping a Service. The audit
// service records each successful mutation to the activity log. authSvc and
// foldersSvc are used by the cross-account copy endpoint, which authorizes the
// actor against — and recreates folder trees in — a tenant other than the one
// their session is bound to.
func NewHandlers(svc *Service, auditSvc *audit.Service, authSvc *auth.Service, foldersSvc *folders.Service) *Handlers {
	return &Handlers{svc: svc, audit: auditSvc, auth: authSvc, folders: foldersSvc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the file routes onto a mux-like target. Files are always
// tenant-scoped — every route is wrapped by RequireAuth.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("POST /api/files", requireAuth(http.HandlerFunc(h.Upload)))
	m.Handle("GET /api/files", requireAuth(http.HandlerFunc(h.List)))
	m.Handle("GET /api/files/recent", requireAuth(http.HandlerFunc(h.Recent)))
	m.Handle("GET /api/files/recent/page", requireAuth(http.HandlerFunc(h.RecentPage)))
	m.Handle("GET /api/files/stats", requireAuth(http.HandlerFunc(h.Stats)))
	m.Handle("GET /api/files/tree-stats", requireAuth(http.HandlerFunc(h.TreeStats)))
	m.Handle("GET /api/files/starred/page", requireAuth(http.HandlerFunc(h.StarredPage)))
	m.Handle("GET /api/files/starred/stats", requireAuth(http.HandlerFunc(h.StarredStats)))
	m.Handle("GET /api/files/trash", requireAuth(http.HandlerFunc(h.Trash)))
	m.Handle("GET /api/files/quota", requireAuth(http.HandlerFunc(h.Quota)))
	m.Handle("GET /api/files/{id}/download", requireAuth(http.HandlerFunc(h.Download)))
	m.Handle("PUT /api/files/{id}/content", requireAuth(http.HandlerFunc(h.SaveContent)))
	m.Handle("GET /api/folders/{id}/zip", requireAuth(http.HandlerFunc(h.FolderZip)))
	m.Handle("GET /api/files/{id}/preview.pdf", requireAuth(http.HandlerFunc(h.PreviewPDF)))
	m.Handle("GET /api/files/{id}/thumb", requireAuth(http.HandlerFunc(h.Thumb)))
	m.Handle("PATCH /api/files/{id}", requireAuth(http.HandlerFunc(h.Patch)))
	m.Handle("DELETE /api/files/{id}", requireAuth(http.HandlerFunc(h.Delete)))
	m.Handle("GET /api/files/name-check", requireAuth(http.HandlerFunc(h.NameCheck)))
	// Resumable chunked uploads (large files; survive a dropped connection). Kept
	// under /api/uploads so the {token} wildcard can't collide with /api/files/{id}.
	m.Handle("POST /api/uploads/init", requireAuth(http.HandlerFunc(h.UploadInit)))
	m.Handle("GET /api/uploads/{token}", requireAuth(http.HandlerFunc(h.UploadStatus)))
	m.Handle("PUT /api/uploads/{token}/chunk/{index}", requireAuth(http.HandlerFunc(h.UploadChunk)))
	m.Handle("POST /api/uploads/{token}/complete", requireAuth(http.HandlerFunc(h.UploadComplete)))
	m.Handle("DELETE /api/uploads/{token}", requireAuth(http.HandlerFunc(h.UploadAbort)))
	m.Handle("POST /api/files/copy-to-account", requireAuth(http.HandlerFunc(h.CopyToAccount)))
	m.Handle("POST /api/files/copy", requireAuth(http.HandlerFunc(h.Copy)))
	m.Handle("GET /api/search", requireAuth(http.HandlerFunc(h.Search)))
}

// ----- handlers ------------------------------------------------------------

type errorResp struct {
	Error string `json:"error"`
}

func (h *Handlers) Quota(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	q, err := h.svc.Quota(r.Context(), identity.Tenant.ID)
	if err != nil {
		logging.Error(packageName, "Quota failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// List returns files in a specific folder (or root if folder_id is omitted).
// TreeStats reports the recursive folder/file counts under a location — the
// Files status bar's 'Total: N items' line.
func (h *Handlers) TreeStats(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	folderID, err := parseOptionalInt64(r.URL.Query().Get("folder_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid folder_id"})
		return
	}
	workspaceID, err := parseOptionalInt64(r.URL.Query().Get("workspace_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid workspace_id"})
		return
	}
	if !identity.Access.CanReadAt(folderID, workspaceID) {
		writeJSON(w, http.StatusOK, TreeStats{}) // degrade like List does at root
		return
	}
	st, err := h.svc.TreeStatsFor(r.Context(), identity.Tenant.ID, folderID, workspaceID)
	if err != nil {
		logging.Error(packageName, "TreeStats failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	folderID, err := parseOptionalInt64(r.URL.Query().Get("folder_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid folder_id"})
		return
	}
	if !identity.Access.CanRead(folderID) {
		// A scoped member has no blanket access to the tenant root, but the
		// folder itself isn't a 403-worthy secret — return an empty listing
		// at root so the UI degrades gracefully, and 403 only for an explicit
		// folder they aren't granted.
		if folderID == nil {
			writeJSON(w, http.StatusOK, map[string]any{"files": []*File{}})
			return
		}
		writeJSON(w, http.StatusForbidden, errorResp{Error: "forbidden"})
		return
	}
	files, err := h.svc.ListInFolder(r.Context(), identity.Tenant.ID, folderID)
	if err != nil {
		logging.Error(packageName, "List failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// Recent returns the N most recently uploaded active files across all folders.
func (h *Handlers) Recent(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	files, err := h.svc.ListRecent(r.Context(), identity.Tenant.ID, limit, folderScope(identity))
	if err != nil {
		logging.Error(packageName, "Recent failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// parseExtFilter reads the optional file-type filter from a request:
//
//	?ext=pdf                          -> only .pdf files
//	?ext=__other__&exclude=pdf,docx   -> files whose ext is none of those
//
// Empty ext means no filter. Excluded values are trimmed and lowercased.
func parseExtFilter(r *http.Request) (string, []string) {
	ext := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("ext")))
	var exclude []string
	if raw := r.URL.Query().Get("exclude"); raw != "" {
		for _, e := range strings.Split(raw, ",") {
			if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
				exclude = append(exclude, e)
			}
		}
	}
	return ext, exclude
}

// RecentPage returns a paginated slice of recent files plus the tenant
// total. Query params: ?offset=N&limit=N (limit clamped to [1,100]).
func (h *Handlers) RecentPage(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	sort := r.URL.Query().Get("sort")
	dir := r.URL.Query().Get("dir")
	ext, exclude := parseExtFilter(r)
	page, err := h.svc.ListRecentPage(r.Context(), identity.Tenant.ID, offset, limit, sort, dir, ext, exclude, folderScope(identity))
	if err != nil {
		logging.Error(packageName, "RecentPage failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// Stats returns tenant-wide active-file counts grouped by extension.
func (h *Handlers) Stats(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	stats, err := h.svc.Stats(r.Context(), identity.Tenant.ID, folderScope(identity))
	if err != nil {
		logging.Error(packageName, "Stats failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// StarredPage returns the tenant's starred files, paginated and sortable.
func (h *Handlers) StarredPage(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	sort := r.URL.Query().Get("sort")
	dir := r.URL.Query().Get("dir")
	ext, exclude := parseExtFilter(r)
	page, err := h.svc.ListStarredPage(r.Context(), identity.Tenant.ID, offset, limit, sort, dir, ext, exclude, folderScope(identity))
	if err != nil {
		logging.Error(packageName, "StarredPage failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// StarredStats returns the starred-only extension breakdown.
func (h *Handlers) StarredStats(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	stats, err := h.svc.StatsStarred(r.Context(), identity.Tenant.ID, folderScope(identity))
	if err != nil {
		logging.Error(packageName, "StarredStats failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// Trash returns the tenant's soft-deleted files.
func (h *Handlers) Trash(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	files, err := h.svc.ListTrashed(r.Context(), identity.Tenant.ID, folderScope(identity))
	if err != nil {
		logging.Error(packageName, "Trash failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// Search returns files whose name matches q (case-insensitive substring),
// tenant-wide. Useful for the search box in the UI.
func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	q := r.URL.Query().Get("q")
	results, err := h.svc.Search(r.Context(), identity.Tenant.ID, q, folderScope(identity))
	if err != nil {
		logging.Error(packageName, "Search failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": results, "query": q})
}

func (h *Handlers) Upload(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}

	// Large uploads stream in over many seconds. Replace the server's blanket
	// 15s ReadTimeout with an inactivity-based deadline so a big (but steadily
	// arriving) file isn't truncated mid-upload — see transferIdleTimeout.
	r.Body = httpx.NewIdleReader(w, r.Body, httpx.DefaultTransferIdleTimeout)

	// Streaming passthrough: we read the multipart body part-by-part and pipe each
	// file part STRAIGHT into the storage layer (where it is encrypted in 64 KiB
	// frames and block-streamed to Azure). The bytes never settle to local disk —
	// memory stays bounded regardless of file size, and there's no temp-disk
	// ceiling on how large or how many concurrent uploads we can take.
	//
	// Parts are consumed in order, so the client MUST send the metadata fields
	// (folder_id, workspace_id) BEFORE the file part(s): the access check has to
	// run before any file bytes flow. See interface/src/lib/api.ts (file appended
	// last). Per-file size and quota limits are still enforced by Service.Upload.
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "expected multipart/form-data"})
		return
	}

	var (
		folderID, workspaceID *int64
		accessChecked         bool
		uploaded              = make([]*File, 0, 1)
	)

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid multipart body"})
			return
		}

		switch part.FormName() {
		case "folder_id":
			v, e := readSmallField(part)
			part.Close()
			if e != nil {
				writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid multipart body"})
				return
			}
			if folderID, e = parseOptionalInt64(v); e != nil {
				writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid folder_id"})
				return
			}
		case "workspace_id":
			v, e := readSmallField(part)
			part.Close()
			if e != nil {
				writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid multipart body"})
				return
			}
			if workspaceID, e = parseOptionalInt64(v); e != nil {
				writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid workspace_id"})
				return
			}
		case "file":
			filename := part.FileName()
			if filename == "" {
				part.Close()
				writeJSON(w, http.StatusBadRequest, errorResp{Error: "missing file name"})
				return
			}
			// Authorize once, before streaming any bytes (folder_id/workspace_id
			// have already been read because the client sends them first).
			if !accessChecked {
				if !identity.Access.CanWriteAt(folderID, workspaceID) {
					part.Close()
					writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
					return
				}
				accessChecked = true
			}
			f, uErr := h.svc.Upload(r.Context(), UploadInput{
				TenantID:       identity.Tenant.ID,
				UploaderID:     identity.User.ID,
				Name:           filename,
				ContentType:    part.Header.Get("Content-Type"),
				Reader:         part, // the live network stream — no temp file
				ParentFolderID: folderID,
				WorkspaceID:    workspaceID,
			})
			part.Close()
			if uErr != nil {
				writeJSON(w, statusForFileErr(uErr), map[string]any{
					"error":    uErr.Error(),
					"uploaded": uploaded,
					"failed":   filename,
				})
				return
			}
			uploaded = append(uploaded, f)
		default:
			part.Close() // ignore unexpected fields, keep the stream in sync
		}
	}

	if len(uploaded) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "missing file field"})
		return
	}

	itemPath := h.audit.PathOf(r.Context(), identity.Tenant.ID, folderID)
	for _, f := range uploaded {
		fileID := f.ID
		h.record(r, identity, audit.RecordInput{
			Action: audit.ActionUpload, TargetID: &fileID,
			ItemName: f.Name, ItemPath: itemPath,
		})
	}

	writeJSON(w, http.StatusCreated, map[string]any{"files": uploaded})
}

// readSmallField reads a non-file multipart form field (folder_id, workspace_id).
// It caps the read at 1 KiB so a malformed/oversized "field" can't be used to
// buffer unbounded data into memory.
func readSmallField(part *multipart.Part) (string, error) {
	b, err := io.ReadAll(io.LimitReader(part, 1<<10))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// FolderZip streams a folder's entire subtree as a zip archive — powers
// dragging a Trilli folder out to the desktop (and any "download folder" UI).
func (h *Handlers) FolderZip(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusForbidden, errorResp{Error: "no access to this folder"})
		return
	}
	name, err := h.svc.FolderZipName(r.Context(), identity.Tenant.ID, folderID)
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	fzID := folderID
	h.record(r, identity, audit.RecordInput{
		Action: audit.ActionDownload, TargetID: &fzID, ItemName: name + ".zip",
		ItemPath: h.audit.PathOf(r.Context(), identity.Tenant.ID, &folderID),
	})
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, sanitizeFilename(name)))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	n, err := h.svc.WriteFolderZip(r.Context(), identity.Tenant.ID, folderID,
		httpx.NewIdleWriter(w, httpx.DefaultTransferIdleTimeout))
	if err != nil {
		logging.Error(packageName, "folder zip %d: %v", folderID, err)
	}
	h.svc.RecordDownloadBytes(context.WithoutCancel(r.Context()), identity.Tenant.ID, &identity.User.ID, n)
}

func (h *Handlers) Download(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	fileID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if !h.gateFileRead(w, r, identity, fileID) {
		return
	}

	f, rc, err := h.svc.Download(r.Context(), identity.Tenant.ID, fileID)
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	defer rc.Close()

	// Any read — inline preview or explicit download — refreshes the file's
	// access recency for storage-tiering analysis. Best-effort, non-blocking.
	h.svc.TouchAccess(identity.Tenant.ID, f.ID)

	// ?inline=1 serves the bytes for in-app preview (rendered in an <img>/<iframe>
	// etc.) rather than forcing a download. Previews aren't logged as downloads.
	inline := r.URL.Query().Get("inline") == "1"
	if !inline {
		dlID := f.ID
		h.record(r, identity, audit.RecordInput{
			Action: audit.ActionDownload, TargetID: &dlID, ItemName: f.Name,
			ItemPath: h.audit.PathOf(r.Context(), identity.Tenant.ID, f.ParentFolderID),
		})
	}

	ct := f.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.FormatInt(f.SizeBytes, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, sanitizeFilename(f.Name)))
	// Don't cache downloads — an overwritten file has the same URL but new
	// content, so immutable caching would serve stale bytes. no-cache forces
	// revalidation on every request.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	// Stream under an inactivity deadline instead of the server's absolute 15s
	// WriteTimeout, so large downloads to ordinary connections aren't cut off.
	n, err := io.Copy(httpx.NewIdleWriter(w, httpx.DefaultTransferIdleTimeout), rc)
	if err != nil {
		logging.Error(packageName, "Download copy failed: %v", err)
	}
	// Meter egress for the bytes actually streamed (over-cap never blocks reads).
	// Detach from the request context — by now the response is sent and the
	// client may have disconnected, which would otherwise cancel the write.
	h.svc.RecordDownloadBytes(context.WithoutCancel(r.Context()), identity.Tenant.ID, &identity.User.ID, n)
}

// PreviewPDF streams a PDF rendering of an Office document (Word/Excel/
// PowerPoint and friends) for the in-app viewer. The original file is never
// altered — the PDF is a cached derivative. Not logged as a download and not
// metered: it's an internal, in-infrastructure preview, not user egress.
func (h *Handlers) PreviewPDF(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	fileID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if !h.gateFileRead(w, r, identity, fileID) {
		return
	}

	rc, err := h.svc.PreviewPDF(r.Context(), identity.Tenant.ID, fileID)
	if err != nil {
		switch {
		case errors.Is(err, ErrPreviewUnavailable):
			writeJSON(w, http.StatusUnsupportedMediaType, errorResp{Error: "preview not available for this file"})
		case errors.Is(err, docpreview.ErrTooLarge):
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResp{Error: "document is too large to preview"})
		case errors.Is(err, docpreview.ErrConversionFailed):
			logging.Error(packageName, "PreviewPDF conversion failed (file=%d): %v", fileID, err)
			writeJSON(w, http.StatusBadGateway, errorResp{Error: "couldn't render a preview for this document"})
		default:
			writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		}
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// An inactivity deadline (not the absolute 15s WriteTimeout) covers BOTH the
	// time spent converting before the first byte and the streaming after it:
	// resetting the write deadline on the first write recovers even when the
	// conversion already ran past the server's default window.
	if _, err := io.Copy(httpx.NewIdleWriter(w, httpx.DefaultTransferIdleTimeout), rc); err != nil {
		logging.Error(packageName, "PreviewPDF copy failed (file=%d): %v", fileID, err)
	}
}

// Thumb streams a small JPEG thumbnail of an image/PDF/video file for the
// list views. Like PreviewPDF it's an internal cached derivative: not logged
// as a download, not metered as egress, and deliberately NOT touching the
// file's access recency — rendering a folder listing must never look like
// the files in it were read (it would defeat storage tiering).
func (h *Handlers) Thumb(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	fileID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if !h.gateFileRead(w, r, identity, fileID) {
		return
	}

	rc, version, err := h.svc.Thumb(r.Context(), identity.Tenant.ID, fileID)
	if err != nil {
		switch {
		case errors.Is(err, thumbs.ErrUnsupported), errors.Is(err, thumbs.ErrTooLarge):
			// Quietly: the row's <img> onerror falls back to the type icon.
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, thumbs.ErrGenerationFailed):
			logging.Debug(packageName, "thumb generation failed (file=%d): %v", fileID, err)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		}
		return
	}
	defer rc.Close()

	// Thumbnails are immutable per version (the URL carries ?v=updated_at),
	// so the browser can cache them hard and skip re-fetching on every visit.
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=604800, immutable")
	w.Header().Set("ETag", `"`+version+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(httpx.NewIdleWriter(w, httpx.DefaultTransferIdleTimeout), rc); err != nil {
		logging.Debug(packageName, "thumb copy failed (file=%d): %v", fileID, err)
	}
}

// Patch handles rename + move + star via a single endpoint. Body fields
// are optional:
//
//	{"name": "newname"}                            // rename
//	{"folder_id": 5}                               // move into folder 5
//	{"folder_id": null}                            // move to tenant root
//	{"starred": true}                              // star the file
//	{"starred": false}                             // unstar the file
//	{"name": "x", "folder_id": 5}                  // rename + move
type patchFileReq struct {
	Name            *string `json:"name"`
	FolderID        *int64  `json:"folder_id"`
	FolderIDPresent bool    `json:"-"`
	WorkspaceID     *int64  `json:"workspace_id"` // target workspace for a root move (cross-workspace transfer)
	Starred         *bool   `json:"starred"`
}

// SaveContent replaces a file's bytes in-place with the request body — the
// save path for in-preview editors (JSON viewer). Same auth as any write at
// the file's location; quota/transfer accounting via OverwriteFile.
func (h *Handlers) SaveContent(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	fileID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	folderID, workspaceID, name, ctype, prot, err := h.svc.ContentLocation(r.Context(), identity.Tenant.ID, fileID)
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	if prot != "" {
		writeJSON(w, http.StatusForbidden, errorResp{Error: ErrFileProtected.Error()})
		return
	}
	if identity.Access != nil && !identity.Access.CanWriteAt(folderID, &workspaceID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && ct != "application/octet-stream" {
		ctype = ct
	}
	body := http.MaxBytesReader(w, r.Body, 16<<20) // in-preview edits are text; 16 MiB is plenty
	f, err := h.svc.OverwriteFile(r.Context(), identity.Tenant.ID, fileID, name, ctype, body, identity.User.ID)
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *Handlers) Patch(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	fileID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}

	// Custom decode so we can tell "folder_id missing" from "folder_id: null".
	raw := map[string]json.RawMessage{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid JSON body"})
		return
	}

	var req patchFileReq
	if rawName, ok := raw["name"]; ok {
		var s string
		if err := json.Unmarshal(rawName, &s); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid name"})
			return
		}
		req.Name = &s
	}
	if rawFolder, ok := raw["folder_id"]; ok {
		req.FolderIDPresent = true
		if string(rawFolder) != "null" {
			var id int64
			if err := json.Unmarshal(rawFolder, &id); err != nil {
				writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid folder_id"})
				return
			}
			req.FolderID = &id
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

	// Gate before any mutation. Every Patch op writes the file's current
	// folder; a move additionally writes the destination folder. Capture the
	// current name + folder once — used for both the gate and the audit
	// snapshot (e.g. rename's "was <old name>").
	oldName, currentFolderID, err := h.svc.FileMeta(r.Context(), identity.Tenant.ID, fileID)
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	if !identity.Access.CanWrite(currentFolderID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
		return
	}
	if req.FolderIDPresent && !identity.Access.CanWriteAt(req.FolderID, req.WorkspaceID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
		return
	}

	var result *File
	if req.Name != nil {
		f, err := h.svc.Rename(r.Context(), identity.Tenant.ID, fileID, *req.Name)
		if err != nil {
			writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
			return
		}
		result = f
		prev := oldName
		h.record(r, identity, audit.RecordInput{
			Action: audit.ActionRename, TargetID: &fileID, ItemName: f.Name, OldName: &prev,
			ItemPath: h.audit.PathOf(r.Context(), identity.Tenant.ID, f.ParentFolderID),
		})
	}
	if req.FolderIDPresent {
		f, err := h.svc.Move(r.Context(), identity.Tenant.ID, fileID, req.FolderID, req.WorkspaceID)
		if err != nil {
			writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
			return
		}
		result = f
		from := h.audit.PathOf(r.Context(), identity.Tenant.ID, currentFolderID)
		to := h.audit.PathOf(r.Context(), identity.Tenant.ID, f.ParentFolderID)
		h.record(r, identity, audit.RecordInput{
			Action: audit.ActionMove, TargetID: &fileID, ItemName: f.Name,
			ItemPath: to, FromPath: &from, ToPath: &to,
		})
	}
	if req.Starred != nil {
		f, err := h.svc.SetStarred(r.Context(), identity.Tenant.ID, fileID, *req.Starred)
		if err != nil {
			writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
			return
		}
		result = f
		// Only a fresh star is interesting in the activity feed; unstarring
		// isn't recorded (it would just be noise).
		if *req.Starred {
			h.record(r, identity, audit.RecordInput{
				Action: audit.ActionStar, TargetID: &fileID, ItemName: f.Name,
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

func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	fileID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	name, folderID, err := h.svc.FileMeta(r.Context(), identity.Tenant.ID, fileID)
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	if !identity.Access.CanWrite(folderID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
		return
	}
	itemPath := h.audit.PathOf(r.Context(), identity.Tenant.ID, folderID)
	if err := h.svc.Delete(r.Context(), identity.Tenant.ID, fileID, identity.User.ID); err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return
	}
	h.record(r, identity, audit.RecordInput{
		Action: audit.ActionDelete, TargetID: &fileID, ItemName: name, ItemPath: itemPath,
	})
	w.WriteHeader(http.StatusNoContent)
}

// ----- folder-access gating -------------------------------------------------

// gateFileRead resolves the file's folder and enforces read access. On
// denial it writes the response and returns false. A missing/trashed file
// maps to the usual ErrFileNotFound status so existence isn't leaked
// differently for scoped vs. full members.
func (h *Handlers) gateFileRead(w http.ResponseWriter, r *http.Request, identity *auth.Identity, fileID int64) bool {
	_, folderID, err := h.svc.FileMeta(r.Context(), identity.Tenant.ID, fileID)
	if err != nil {
		writeJSON(w, statusForFileErr(err), errorResp{Error: err.Error()})
		return false
	}
	if !identity.Access.CanRead(folderID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: "You don't have access to this folder"})
		return false
	}
	return true
}

// folderScope translates an identity's access into a FolderScope for the
// tenant-wide listings (recent, starred, search, trash, stats). Owner/admin
// (unrestricted) get nil — no filtering. A scoped member/viewer gets their
// accessible folder set, so these views never surface files outside the
// folders they were granted.
func folderScope(identity *auth.Identity) *FolderScope {
	if identity.Access == nil || !identity.Access.Restricted() {
		return nil
	}
	return &FolderScope{IDs: identity.Access.FolderIDs()}
}

// NameCheck reports whether moving a file named `name` into the given
// destination (optional folder_id; absent/null = workspace root) would collide
// with an existing file there, plus the suggested non-colliding name (what a
// move would auto-assign). Powers the pre-move "a file with that name already
// exists" prompt. Read-only.
func (h *Handlers) NameCheck(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "name required"})
		return
	}
	var folderID *int64
	if raw := strings.TrimSpace(r.URL.Query().Get("folder_id")); raw != "" && raw != "0" && raw != "null" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid folder_id"})
			return
		}
		folderID = &id
	}
	if !identity.Access.CanWrite(folderID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: forbiddenWriteMsg(identity)})
		return
	}
	available, suggested, err := h.svc.NameStatus(r.Context(), identity.Tenant.ID, folderID, name)
	if err != nil {
		logging.Error("files", "NameCheck: tenant=%d: %v", identity.Tenant.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": available, "suggested": suggested})
}

// copyToAccountReq is the body for POST /api/files/copy-to-account: copy the
// listed files/folders from the caller's CURRENT account into another account
// they belong to. Cross-account transfers are always copies (never moves) — the
// source is left untouched.
type copyToAccountReq struct {
	FileIDs         []int64 `json:"file_ids"`
	FolderIDs       []int64 `json:"folder_ids"`
	DestTenantID    int64   `json:"dest_tenant_id"`
	DestWorkspaceID *int64  `json:"dest_workspace_id"` // for copies into the dest account root
	DestFolderID    *int64  `json:"dest_folder_id"`    // nil = dest workspace root
}

type copyToAccountResp struct {
	CopiedFiles   int      `json:"copied_files"`
	CopiedFolders int      `json:"copied_folders"`
	Errors        []string `json:"errors,omitempty"`
}

// CopyToAccount copies files/folders from the caller's current account into a
// DIFFERENT account they're an active member of. The source account is the
// session tenant (read-gated by identity.Access); the destination is authorized
// separately via AccessForTenant, since a session is bound to one tenant at a
// time. Folders are copied recursively. Best-effort per item: a failure on one
// item is collected into the response and the rest proceed.
func (h *Handlers) CopyToAccount(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req copyToAccountReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request body"})
		return
	}
	srcTenant := identity.Tenant.ID
	if req.DestTenantID == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "destination account is required"})
		return
	}
	if req.DestTenantID == srcTenant {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "use move to relocate within the same account"})
		return
	}
	if len(req.FileIDs) == 0 && len(req.FolderIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "nothing to copy"})
		return
	}

	// Authorize the destination account: the actor must be an active member with
	// write access to the destination folder there. (The source is the session
	// tenant, already gated by identity.Access below.)
	destAccess, destTenant, _, err := h.auth.AccessForTenant(r.Context(), identity.User.ID, req.DestTenantID)
	if errors.Is(err, auth.ErrUnauthorized) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: "You don't have access to that account"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "couldn't open destination account"})
		return
	}
	if !destAccess.CanWriteAt(req.DestFolderID, req.DestWorkspaceID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: "You can't add files to that location"})
		return
	}

	ctx := r.Context()
	destLabel := destTenant.Name
	resp := copyToAccountResp{}

	for _, fid := range req.FileIDs {
		name, parentID, err := h.svc.FileMeta(ctx, srcTenant, fid)
		if err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("file %d: %v", fid, err))
			continue
		}
		if identity.Access != nil && !identity.Access.CanRead(parentID) {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: no access", name))
			continue
		}
		if _, err := h.svc.CopyToTenant(ctx, CrossCopyInput{
			SourceTenantID:  srcTenant,
			SourceFileID:    fid,
			DestTenantID:    req.DestTenantID,
			DestFolderID:    req.DestFolderID,
			DestWorkspaceID: req.DestWorkspaceID,
			ActorUserID:     identity.User.ID,
		}); err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		resp.CopiedFiles++
		fidCopy := fid
		h.record(r, identity, audit.RecordInput{
			Action:   audit.ActionCopy,
			TargetID: &fidCopy,
			ItemName: name,
			ToPath:   &destLabel,
		})
	}

	for _, fid := range req.FolderIDs {
		nf, nfiles, ferr := h.copyFolderToAccount(ctx, identity, srcTenant, fid, req.DestTenantID, req.DestFolderID, req.DestWorkspaceID)
		resp.CopiedFiles += nfiles
		resp.CopiedFolders += nf
		if ferr != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("folder %d: %v", fid, ferr))
			continue
		}
		fidCopy := fid
		h.audit.Record(ctx, audit.RecordInput{
			TenantID:    srcTenant,
			ActorUserID: identity.User.ID,
			ActorEmail:  identity.User.Email,
			Action:      audit.ActionCopy,
			TargetType:  audit.TargetFolder,
			TargetID:    &fidCopy,
			ToPath:      &destLabel,
			Device:      audit.DeviceFromUA(r.UserAgent()),
			IP:          auth.ClientIP(r),
		})
	}

	logging.Info("files", "CopyToAccount: src=%d -> dest=%d files=%d folders=%d errs=%d by=%d",
		srcTenant, req.DestTenantID, resp.CopiedFiles, resp.CopiedFolders, len(resp.Errors), identity.User.ID)
	writeJSON(w, http.StatusOK, resp)
}

// copyFolderToAccount recursively recreates a source folder subtree in the
// destination account and copies every file it contains. destParentID is the
// already-resolved destination parent (nil = dest workspace root); destWS is
// only consulted for the top-level folder — nested folders inherit their new
// parent's workspace. Returns the number of folders and files copied (even on a
// partial failure, so the caller can report progress).
func (h *Handlers) copyFolderToAccount(ctx context.Context, identity *auth.Identity, srcTenant, srcFolderID, destTenant int64, destParentID, destWS *int64) (folderCount, fileCount int, err error) {
	if identity.Access != nil && !identity.Access.CanRead(&srcFolderID) {
		return 0, 0, fmt.Errorf("no access")
	}
	src, err := h.folders.Get(ctx, srcTenant, srcFolderID)
	if err != nil {
		return 0, 0, err
	}

	dest, err := h.createFolderDedup(ctx, destTenant, destParentID, destWS, src.Name, identity.User.ID)
	if err != nil {
		return 0, 0, err
	}
	folderCount = 1

	srcFiles, err := h.svc.ListInFolder(ctx, srcTenant, &srcFolderID)
	if err != nil {
		return folderCount, fileCount, err
	}
	for _, f := range srcFiles {
		if _, cerr := h.svc.CopyToTenant(ctx, CrossCopyInput{
			SourceTenantID: srcTenant,
			SourceFileID:   f.ID,
			DestTenantID:   destTenant,
			DestFolderID:   &dest.ID,
			ActorUserID:    identity.User.ID,
		}); cerr != nil {
			return folderCount, fileCount, cerr
		}
		fileCount++
	}

	children, err := h.folders.ListChildren(ctx, srcTenant, &srcFolderID)
	if err != nil {
		return folderCount, fileCount, err
	}
	for _, c := range children {
		cf, cfiles, cerr := h.copyFolderToAccount(ctx, identity, srcTenant, c.ID, destTenant, &dest.ID, nil)
		folderCount += cf
		fileCount += cfiles
		if cerr != nil {
			return folderCount, fileCount, cerr
		}
	}
	return folderCount, fileCount, nil
}

// createFolderDedup creates a folder in the destination, auto-renaming on a name
// collision ("<name> (copy)", "<name> (copy 2)", …) so a cross-account copy
// never fails just because the destination already holds a folder by that name.
func (h *Handlers) createFolderDedup(ctx context.Context, tenantID int64, parentID, wsID *int64, name string, userID int64) (*folders.Folder, error) {
	for i := 0; i <= 99; i++ {
		candidate := name
		if i == 1 {
			candidate = name + " (copy)"
		} else if i >= 2 {
			candidate = fmt.Sprintf("%s (copy %d)", name, i)
		}
		f, err := h.folders.Create(ctx, folders.CreateInput{
			TenantID:        tenantID,
			ParentFolderID:  parentID,
			WorkspaceID:     wsID,
			Name:            candidate,
			CreatedByUserID: userID,
		})
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, folders.ErrNameTaken) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("too many copies of folder %q", name)
}

// copyReq is the body for POST /api/files/copy — a SAME-account copy. Used by
// both Duplicate (a single item, Name set, dest = its own folder) and
// Copy/Paste (any number of items, no Name, dest = the folder pasted into).
type copyReq struct {
	FileIDs         []int64 `json:"file_ids"`
	FolderIDs       []int64 `json:"folder_ids"`
	DestFolderID    *int64  `json:"dest_folder_id"`    // nil = workspace root
	DestWorkspaceID *int64  `json:"dest_workspace_id"` // consulted for root copies
	Name            string  `json:"name"`              // honored only for a single item (Duplicate)
}

type copyResp struct {
	CopiedFiles   int      `json:"copied_files"`
	CopiedFolders int      `json:"copied_folders"`
	Errors        []string `json:"errors,omitempty"`
}

// Copy duplicates files/folders WITHIN the caller's current account. A name is
// kept when free in the destination and auto-renamed "<base>_copy_NN<ext>" only
// on a clash — so a paste into another folder keeps the name while a duplicate
// (or a paste into the source folder) renames. Folders copy recursively. Counts
// against this account's quota. Best-effort per item.
func (h *Handlers) Copy(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req copyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request body"})
		return
	}
	if len(req.FileIDs) == 0 && len(req.FolderIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "nothing to copy"})
		return
	}
	if identity.Access != nil && !identity.Access.CanWriteAt(req.DestFolderID, req.DestWorkspaceID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: "You can't add items to that location"})
		return
	}

	tenant := identity.Tenant.ID
	ctx := r.Context()
	// An explicit name only applies to a single item (the Duplicate dialog).
	explicitName := ""
	if len(req.FileIDs)+len(req.FolderIDs) == 1 {
		explicitName = strings.TrimSpace(req.Name)
	}
	resp := copyResp{}

	for _, fid := range req.FileIDs {
		name, parentID, err := h.svc.FileMeta(ctx, tenant, fid)
		if err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("file %d: %v", fid, err))
			continue
		}
		if identity.Access != nil && !identity.Access.CanRead(parentID) {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: no access", name))
			continue
		}
		nf, err := h.svc.Copy(ctx, CopyInput{
			TenantID:        tenant,
			SourceFileID:    fid,
			DestFolderID:    req.DestFolderID,
			DestWorkspaceID: req.DestWorkspaceID,
			ActorUserID:     identity.User.ID,
			Name:            explicitName,
		})
		if err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		resp.CopiedFiles++
		nid := nf.ID
		h.record(r, identity, audit.RecordInput{
			Action:   audit.ActionCopy,
			TargetID: &nid,
			ItemName: nf.Name,
		})
	}

	for _, fid := range req.FolderIDs {
		// Stop a folder being copied into itself or a descendant — it would
		// recurse forever and runaway-duplicate data.
		if err := h.folders.AssertNotSelfOrDescendant(ctx, tenant, fid, req.DestFolderID); err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("folder %d: can't copy a folder into itself", fid))
			continue
		}
		nfolders, nfiles, ferr := h.copyFolderSameAccount(ctx, identity, tenant, fid, req.DestFolderID, req.DestWorkspaceID, explicitName)
		resp.CopiedFiles += nfiles
		resp.CopiedFolders += nfolders
		if ferr != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("folder %d: %v", fid, ferr))
			continue
		}
		fidCopy := fid
		h.audit.Record(ctx, audit.RecordInput{
			TenantID:    tenant,
			ActorUserID: identity.User.ID,
			ActorEmail:  identity.User.Email,
			Action:      audit.ActionCopy,
			TargetType:  audit.TargetFolder,
			TargetID:    &fidCopy,
			Device:      audit.DeviceFromUA(r.UserAgent()),
			IP:          auth.ClientIP(r),
		})
	}

	logging.Info("files", "Copy: tenant=%d files=%d folders=%d errs=%d by=%d",
		tenant, resp.CopiedFiles, resp.CopiedFolders, len(resp.Errors), identity.User.ID)
	writeJSON(w, http.StatusOK, resp)
}

// copyFolderSameAccount recursively recreates a folder subtree within the same
// account. name overrides the TOP folder's name (the Duplicate dialog); nested
// folders keep their names (they can't clash in a freshly-created destination).
func (h *Handlers) copyFolderSameAccount(ctx context.Context, identity *auth.Identity, tenant, srcFolderID int64, destParentID, destWS *int64, name string) (folderCount, fileCount int, err error) {
	if identity.Access != nil && !identity.Access.CanRead(&srcFolderID) {
		return 0, 0, fmt.Errorf("no access")
	}
	src, err := h.folders.Get(ctx, tenant, srcFolderID)
	if err != nil {
		return 0, 0, err
	}
	folderName := src.Name
	if name != "" {
		folderName = name
	}
	dest, err := h.folders.CreateCopy(ctx, folders.CreateInput{
		TenantID:        tenant,
		ParentFolderID:  destParentID,
		WorkspaceID:     destWS,
		Name:            folderName,
		CreatedByUserID: identity.User.ID,
	})
	if err != nil {
		return 0, 0, err
	}
	folderCount = 1

	srcFiles, err := h.svc.ListInFolder(ctx, tenant, &srcFolderID)
	if err != nil {
		return folderCount, fileCount, err
	}
	for _, f := range srcFiles {
		if _, cerr := h.svc.Copy(ctx, CopyInput{
			TenantID:     tenant,
			SourceFileID: f.ID,
			DestFolderID: &dest.ID,
			ActorUserID:  identity.User.ID,
		}); cerr != nil {
			return folderCount, fileCount, cerr
		}
		fileCount++
	}

	children, err := h.folders.ListChildren(ctx, tenant, &srcFolderID)
	if err != nil {
		return folderCount, fileCount, err
	}
	for _, c := range children {
		cf, cfiles, cerr := h.copyFolderSameAccount(ctx, identity, tenant, c.ID, &dest.ID, nil, "")
		folderCount += cf
		fileCount += cfiles
		if cerr != nil {
			return folderCount, fileCount, cerr
		}
	}
	return folderCount, fileCount, nil
}

// record appends a file audit event, filling in actor/device/ip from the
// request + identity. Best-effort (audit.Record never fails the caller).
func (h *Handlers) record(r *http.Request, identity *auth.Identity, in audit.RecordInput) {
	in.TenantID = identity.Tenant.ID
	in.ActorUserID = identity.User.ID
	in.ActorEmail = identity.User.Email
	in.TargetType = audit.TargetFile
	in.Device = audit.DeviceFromUA(r.UserAgent())
	in.IP = auth.ClientIP(r)
	h.audit.Record(r.Context(), in)
}

// forbiddenWriteMsg returns a message that distinguishes a read-only
// (viewer) refusal from an out-of-scope folder refusal, so the UI can show
// something more useful than a bare "forbidden".
func forbiddenWriteMsg(identity *auth.Identity) string {
	if identity.Access != nil && !identity.Access.Write {
		return "Your role is read-only — you can't modify files"
	}
	return "You don't have access to this folder"
}

// ----- helpers -------------------------------------------------------------

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

func statusForFileErr(err error) int {
	switch {
	case errors.Is(err, ErrFileProtected), errors.Is(err, ErrFolderProtectedUploads):
		return http.StatusForbidden
	case errors.Is(err, ErrFileNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrFileTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrQuotaExceeded), errors.Is(err, ErrWorkspaceQuotaExceeded):
		return http.StatusInsufficientStorage
	case errors.Is(err, ErrTransferExceeded):
		return http.StatusInsufficientStorage
	case errors.Is(err, ErrNameTaken):
		return http.StatusConflict
	case errors.Is(err, ErrInvalidName), errors.Is(err, ErrInvalidFolder),
		errors.Is(err, ErrChunkOutOfRange), errors.Is(err, ErrChunkSize):
		return http.StatusBadRequest
	case errors.Is(err, ErrUploadNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrUploadCompleted), errors.Is(err, ErrUploadIncomplete):
		return http.StatusConflict
	case errors.Is(err, ErrResumableUnsupported):
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

func parseOptionalInt64(s string) (*int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" || s == "0" {
		return nil, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// sanitizeFilename strips quotes, newlines, and control chars to prevent
// Content-Disposition header injection.
func sanitizeFilename(name string) string {
	out := strings.Builder{}
	out.Grow(len(name))
	for _, r := range name {
		if r < 0x20 || r == '"' || r == '\\' {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
