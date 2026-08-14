package apikeys

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"trilli/system/auth"
	"trilli/system/files"
	"trilli/system/folders"
	"trilli/system/httpx"
	"trilli/system/logging"
)

type Handlers struct {
	svc     *Service
	files   *files.Service
	folders *folders.Service
}

func NewHandlers(svc *Service, filesSvc *files.Service, foldersSvc *folders.Service) *Handlers {
	return &Handlers{svc: svc, files: filesSvc, folders: foldersSvc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

type errorResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Register wires the session-authed management routes (/api/keys, admin+plan
// gated) and the key-authed programmatic routes (/api/v1/*).
func (h *Handlers) Register(m muxLike, requireAdminOrOwner func(http.Handler) http.Handler) {
	m.Handle("GET /api/keys", requireAdminOrOwner(http.HandlerFunc(h.ListKeys)))
	m.Handle("POST /api/keys", requireAdminOrOwner(http.HandlerFunc(h.CreateKey)))
	m.Handle("DELETE /api/keys/{id}", requireAdminOrOwner(http.HandlerFunc(h.RevokeKey)))

	// Public, machine-readable API contract (no key required).
	m.Handle("GET /api/v1/openapi.json", http.HandlerFunc(h.OpenAPI))

	key := h.requireKey
	m.Handle("GET /api/v1/account", key(http.HandlerFunc(h.V1Account)))
	m.Handle("GET /api/v1/files", key(http.HandlerFunc(h.V1ListFiles)))
	m.Handle("POST /api/v1/files", key(http.HandlerFunc(h.V1Upload)))
	m.Handle("GET /api/v1/files/{id}", key(http.HandlerFunc(h.V1FileMeta)))
	m.Handle("GET /api/v1/files/{id}/content", key(http.HandlerFunc(h.V1Download)))
	m.Handle("DELETE /api/v1/files/{id}", key(http.HandlerFunc(h.V1DeleteFile)))
	m.Handle("GET /api/v1/folders", key(http.HandlerFunc(h.V1ListFolders)))
	m.Handle("POST /api/v1/folders", key(http.HandlerFunc(h.V1CreateFolder)))
}

// ----- Management (session-authed, admin/owner) ---------------------------

func (h *Handlers) ListKeys(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	access, _ := h.svc.TenantAPIAccess(r.Context(), identity.Tenant.ID)
	keys, err := h.svc.List(r.Context(), identity.Tenant.ID)
	if err != nil {
		logging.Error(packageName, "ListKeys failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys, "api_access": access})
}

func (h *Handlers) CreateKey(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	access, err := h.svc.TenantAPIAccess(r.Context(), identity.Tenant.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	if !access {
		writeJSON(w, http.StatusForbidden, errorResp{Error: "API access is not included in your plan. Upgrade to Plus or Infinity to use the API."})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request body"})
		return
	}
	key, token, err := h.svc.Create(r.Context(), identity.Tenant.ID, identity.User.ID, body.Name)
	if err != nil {
		if errors.Is(err, ErrInvalidName) {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "Please enter a name for the key."})
			return
		}
		logging.Error(packageName, "CreateKey failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	// The plaintext token is returned exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "key": key})
}

func (h *Handlers) RevokeKey(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if err := h.svc.Revoke(r.Context(), identity.Tenant.ID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errorResp{Error: "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- Bearer-key middleware ----------------------------------------------

type apiCtxKey struct{}

var principalKey = apiCtxKey{}

// requireKey authenticates a Bearer API key and enforces the plan entitlement.
func (h *Handlers) requireKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		var token string
		if strings.HasPrefix(authz, "Bearer ") {
			token = strings.TrimSpace(authz[len("Bearer "):])
		}
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, errorResp{Error: "missing bearer token"})
			return
		}
		p, err := h.svc.Authenticate(r.Context(), token)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, errorResp{Error: "invalid API key"})
			return
		}
		if !p.APIAccess {
			writeJSON(w, http.StatusForbidden, errorResp{Error: "API access is not included in this account's plan"})
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, p)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func principalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

// ----- v1 programmatic surface --------------------------------------------

func (h *Handlers) V1Account(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	q, err := h.files.Quota(r.Context(), p.TenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account_id":         p.TenantID,
		"plan":               p.PlanName,
		"storage_bytes_used": q.StorageBytesUsed,
		"max_storage_bytes":  q.MaxStorageBytes,
		"max_file_size_bytes": q.MaxFileSizeBytes,
	})
}

func (h *Handlers) V1ListFiles(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	folderID := optionalInt64(r.URL.Query().Get("folder_id"))
	list, err := h.files.ListInFolder(r.Context(), p.TenantID, folderID)
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": list})
}

func (h *Handlers) V1FileMeta(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	f, err := h.files.Get(r.Context(), p.TenantID, id)
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *Handlers) V1Download(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	f, rc, err := h.files.Download(r.Context(), p.TenantID, id)
	if err != nil {
		writeFileErr(w, err)
		return
	}
	defer rc.Close()
	ct := f.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.FormatInt(f.SizeBytes, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, f.Name))
	n, err := io.Copy(w, rc)
	if err != nil {
		logging.Error(packageName, "V1Download copy failed: %v", err)
	}
	// Meter egress against the plan's transfer allowance, exactly like the web
	// download — the API is just another interface to the same account.
	// (Over-cap never blocks reads; it only increments the counter.)
	h.files.RecordDownloadBytes(context.WithoutCancel(r.Context()), p.TenantID, nil, n)
}

func (h *Handlers) V1Upload(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	// Stream the upload straight to storage (encrypted in flight) without settling
	// it to local disk, so API clients can push arbitrarily large files. Large
	// uploads arrive over many seconds, so swap the blanket 15s ReadTimeout for an
	// inactivity deadline. folder_id may be supplied as a query param
	// (?folder_id=, order-independent and recommended) or as a form field sent
	// BEFORE the file part; a folder_id field sent after the file is ignored.
	r.Body = httpx.NewIdleReader(w, r.Body, httpx.DefaultTransferIdleTimeout)
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "expected multipart/form-data with a 'file' field"})
		return
	}
	folderID := optionalInt64(r.URL.Query().Get("folder_id"))
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid multipart body"})
			return
		}
		if part.FormName() == "folder_id" {
			b, _ := io.ReadAll(io.LimitReader(part, 1<<10))
			part.Close()
			if fid := optionalInt64(string(b)); fid != nil {
				folderID = fid
			}
			continue
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		out, err := h.files.Upload(r.Context(), files.UploadInput{
			TenantID:       p.TenantID,
			UploaderID:     p.UserID,
			Name:           part.FileName(),
			ContentType:    part.Header.Get("Content-Type"),
			Reader:         part,
			ParentFolderID: folderID,
		})
		part.Close()
		if err != nil {
			writeFileErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
		return
	}
	writeJSON(w, http.StatusBadRequest, errorResp{Error: "missing 'file' field"})
}

func (h *Handlers) V1DeleteFile(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if err := h.files.Delete(r.Context(), p.TenantID, id, p.UserID); err != nil {
		writeFileErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) V1ListFolders(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	parentID := optionalInt64(r.URL.Query().Get("parent_id"))
	list, err := h.folders.ListChildren(r.Context(), p.TenantID, parentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": list})
}

func (h *Handlers) V1CreateFolder(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var body struct {
		Name     string `json:"name"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request body"})
		return
	}
	out, err := h.folders.Create(r.Context(), folders.CreateInput{
		TenantID:        p.TenantID,
		CreatedByUserID: p.UserID,
		Name:            body.Name,
		ParentFolderID:  body.ParentID,
	})
	if err != nil {
		switch {
		case errors.Is(err, folders.ErrNameTaken):
			writeJSON(w, http.StatusConflict, errorResp{Error: "a folder with that name already exists here"})
		case errors.Is(err, folders.ErrInvalidName):
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid folder name"})
		default:
			writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		}
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// ----- helpers ------------------------------------------------------------

func optionalInt64(s string) *int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "null" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// writeFileErr maps files-service sentinel errors to HTTP statuses.
func writeFileErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrFileNotFound):
		writeJSON(w, http.StatusNotFound, errorResp{Error: "not found"})
	case errors.Is(err, files.ErrFileTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResp{Error: "file exceeds your plan's max file size"})
	case errors.Is(err, files.ErrQuotaExceeded), errors.Is(err, files.ErrWorkspaceQuotaExceeded):
		writeJSON(w, http.StatusInsufficientStorage, errorResp{Error: "storage quota exceeded"})
	case errors.Is(err, files.ErrTransferExceeded):
		writeJSON(w, http.StatusInsufficientStorage, errorResp{Error: "monthly transfer limit reached"})
	case errors.Is(err, files.ErrInvalidName), errors.Is(err, files.ErrInvalidFolder):
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request"})
	default:
		logging.Error(packageName, "file op failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
	}
}
