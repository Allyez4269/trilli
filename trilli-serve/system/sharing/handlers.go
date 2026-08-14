package sharing

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
	"trilli/system/httpx"
	"trilli/system/logging"
	"trilli/system/ratelimit"
)

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

type errorResp struct {
	Error string `json:"error"`
}

// Register wires share routes. The /api/shares management routes require a
// session; the public /api/s/{token} routes are intentionally unauthenticated
// (token + optional password is the access control).
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("POST /api/shares", requireAuth(http.HandlerFunc(h.Create)))
	m.Handle("GET /api/shares", requireAuth(http.HandlerFunc(h.List)))
	m.Handle("GET /api/shares/recipients", requireAuth(http.HandlerFunc(h.Recipients)))
	m.Handle("DELETE /api/shares/{id}", requireAuth(http.HandlerFunc(h.Revoke)))

	m.Handle("GET /api/s/{token}", http.HandlerFunc(h.PublicMeta))
	m.Handle("GET /api/s/{token}/download", http.HandlerFunc(h.PublicDownload))
	m.Handle("GET /api/s/{token}/browse", http.HandlerFunc(h.PublicBrowse))

	// Drop portals (inbound file requests).
	m.Handle("POST /api/portals", requireAuth(http.HandlerFunc(h.CreatePortal)))
	m.Handle("GET /api/portals", requireAuth(http.HandlerFunc(h.ListPortals)))
	m.Handle("PATCH /api/portals/{id}", requireAuth(http.HandlerFunc(h.UpdatePortal)))
	m.Handle("DELETE /api/portals/{id}", requireAuth(http.HandlerFunc(h.DeletePortal)))
	m.Handle("GET /api/p/{token}", http.HandlerFunc(h.PortalMeta))
	m.Handle("POST /api/p/{token}/upload", http.HandlerFunc(h.PortalUpload))
}

// ----- Drop portal management (authenticated) -----------------------------

func (h *Handlers) CreatePortal(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var body struct {
		FolderID       int64  `json:"folder_id"`
		Title          string `json:"title"`
		ExpiresInDays  *int   `json:"expires_in_days"`
		Password       string `json:"password"`
		NotifyOnUpload bool   `json:"notify_on_upload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FolderID == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request body"})
		return
	}
	portal, err := h.svc.CreatePortal(r.Context(), CreatePortalInput{
		TenantID:       identity.Tenant.ID,
		ActorID:        identity.User.ID,
		Access:         identity.Access,
		FolderID:       body.FolderID,
		Title:          body.Title,
		ExpiresInDays:  body.ExpiresInDays,
		Password:       body.Password,
		NotifyOnUpload: body.NotifyOnUpload,
	})
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, portal)
}

func (h *Handlers) ListPortals(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	portals, err := h.svc.ListPortals(r.Context(), identity.Tenant.ID)
	if err != nil {
		logging.Error(packageName, "ListPortals failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"portals": portals})
}

func (h *Handlers) UpdatePortal(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		Title          *string `json:"title"`
		ExpiresInDays  *int    `json:"expires_in_days"`
		Password       *string `json:"password"`
		NotifyOnUpload *bool   `json:"notify_on_upload"`
		Revoked        *bool   `json:"revoked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request body"})
		return
	}
	isAdmin := identity.Membership != nil && auth.IsAdminOrOwner(identity.Membership.Role)
	portal, err := h.svc.UpdatePortal(r.Context(), identity.Tenant.ID, id, identity.User.ID, isAdmin, UpdatePortalInput{
		Title:          body.Title,
		ExpiresInDays:  body.ExpiresInDays,
		Password:       body.Password,
		NotifyOnUpload: body.NotifyOnUpload,
		Revoked:        body.Revoked,
	})
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, portal)
}

func (h *Handlers) DeletePortal(w http.ResponseWriter, r *http.Request) {
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
	isAdmin := identity.Membership != nil && auth.IsAdminOrOwner(identity.Membership.Role)
	if err := h.svc.DeletePortal(r.Context(), identity.Tenant.ID, id, identity.User.ID, isAdmin); err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- Drop portal public (token-gated) -----------------------------------

func (h *Handlers) PortalMeta(w http.ResponseWriter, r *http.Request) {
	rp, err := h.svc.PortalByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"title":        rp.Title,
		"folder_name":  rp.FolderName,
		"has_password": rp.HasPassword,
	})
}

func (h *Handlers) PortalUpload(w http.ResponseWriter, r *http.Request) {
	if !publicLinkLimiter.Allow(ratelimit.ClientIP(r) + "|" + r.PathValue("token")) {
		ratelimit.TooMany(w)
		return
	}
	rp, err := h.svc.PortalByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	if err := h.svc.AuthorizedPortal(rp, r.URL.Query().Get("pw")); err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	// Public uploads stream in over many seconds — swap the server's blanket 15s
	// ReadTimeout for an inactivity deadline so a large drop isn't truncated.
	r.Body = httpx.NewIdleReader(w, r.Body, httpx.DefaultTransferIdleTimeout)
	// Stream the multipart body part-by-part and pipe the file straight into the
	// store (encrypted in flight) — no temp-file settle, so a large public drop
	// isn't bounded by local disk. The portal form sends only the "file" part.
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "expected multipart/form-data"})
		return
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid multipart body"})
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		rec, err := h.svc.AcceptUpload(r.Context(), rp, part.FileName(), part.Header.Get("Content-Type"), part)
		part.Close()
		if err != nil {
			writeJSON(w, statusForUploadErr(err), errorResp{Error: friendlyUploadErr(err)})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"name": rec.Name, "size_bytes": rec.SizeBytes})
		return
	}
	writeJSON(w, http.StatusBadRequest, errorResp{Error: "missing file"})
}

// ----- Authenticated management -------------------------------------------

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var body struct {
		ResourceType  string `json:"resource_type"`
		FileID        *int64 `json:"file_id"`
		FolderID      *int64 `json:"folder_id"`
		GranteeType   string `json:"grantee_type"`
		GranteeUserID *int64 `json:"grantee_user_id"`
		GranteeEmail  string `json:"grantee_email"`
		Capability    string `json:"capability"`
		ExpiresInDays *int   `json:"expires_in_days"`
		Password      string `json:"password"`
		Notify        bool   `json:"notify"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request body"})
		return
	}
	share, err := h.svc.Create(r.Context(), CreateInput{
		TenantID:      identity.Tenant.ID,
		ActorID:       identity.User.ID,
		Access:        identity.Access,
		ResourceType:  body.ResourceType,
		FileID:        body.FileID,
		FolderID:      body.FolderID,
		GranteeType:   body.GranteeType,
		GranteeUserID: body.GranteeUserID,
		GranteeEmail:  body.GranteeEmail,
		Capability:    body.Capability,
		ExpiresInDays: body.ExpiresInDays,
		Password:      body.Password,
		Notify:        body.Notify,
		SharerName:    sharerName(identity),
	})
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, share)
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	shares, err := h.svc.ListForUser(r.Context(), identity.Tenant.ID, identity.User.ID)
	if err != nil {
		logging.Error(packageName, "List failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares})
}

func (h *Handlers) Recipients(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	recipients, err := h.svc.ListRecipients(r.Context(), identity.Tenant.ID, identity.User.ID)
	if err != nil {
		logging.Error(packageName, "Recipients failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipients": recipients})
}

// sharerName returns a display name for the person creating a share.
func sharerName(identity *auth.Identity) string {
	u := identity.User
	if u.FullName != nil && *u.FullName != "" {
		return *u.FullName
	}
	first, last := "", ""
	if u.FirstName != nil {
		first = *u.FirstName
	}
	if u.LastName != nil {
		last = *u.LastName
	}
	full := strings.TrimSpace(first + " " + last)
	if full != "" {
		return full
	}
	return u.Email
}

func (h *Handlers) Revoke(w http.ResponseWriter, r *http.Request) {
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
	isAdmin := identity.Membership != nil && auth.IsAdminOrOwner(identity.Membership.Role)
	// ?hard=1 permanently removes the row; otherwise we soft-revoke (status "Off").
	if hard := r.URL.Query().Get("hard"); hard == "1" || hard == "true" {
		if err := h.svc.Delete(r.Context(), identity.Tenant.ID, id, identity.User.ID, isAdmin); err != nil {
			writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.svc.Revoke(r.Context(), identity.Tenant.ID, id, identity.User.ID, isAdmin); err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- Public (token-gated) -----------------------------------------------

// publicLinkLimiter throttles token+password attempts on the public share /
// portal endpoints per (IP, token). Generous for real use (browsing a shared
// folder is a handful of calls), hopeless for guessing an argon2id-hashed
// link password.
var publicLinkLimiter = ratelimit.PerMinute(30, 60)

func (h *Handlers) PublicMeta(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.ByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	// For password-protected shares, do NOT leak the resource name on the public
	// landing page — it's reachable (and crawlable) without the password. The name
	// is revealed only after the password is verified (via /browse or /download).
	// This also stops automated scanners (e.g. Safe Browsing) from reading a
	// sensitive folder name and mis-flagging the whole domain as deceptive.
	name := res.ResourceName
	if res.HasPassword {
		name = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resource_type": res.ResourceType,
		"resource_name": name,
		"capability":    res.Capability,
		"has_password":  res.HasPassword,
	})
}

func (h *Handlers) PublicDownload(w http.ResponseWriter, r *http.Request) {
	if !publicLinkLimiter.Allow(ratelimit.ClientIP(r) + "|" + r.PathValue("token")) {
		ratelimit.TooMany(w)
		return
	}
	res, err := h.svc.ByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	if err := h.svc.Authorized(res, r.URL.Query().Get("pw")); err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	var fileID int64
	if raw := r.URL.Query().Get("file_id"); raw != "" {
		fileID, _ = strconv.ParseInt(raw, 10, 64)
	}
	sf, rc, err := h.svc.OpenForDownload(r.Context(), res, fileID)
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	defer rc.Close()

	ct := sf.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.FormatInt(sf.SizeBytes, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(sf.Name)))
	// Inactivity deadline instead of the server's absolute 15s WriteTimeout, so
	// large public-link downloads to ordinary connections aren't cut off.
	n, copyErr := io.Copy(httpx.NewIdleWriter(w, httpx.DefaultTransferIdleTimeout), rc)
	if copyErr != nil {
		logging.Error(packageName, "PublicDownload copy failed: %v", copyErr)
	}
	// Meter egress + bump access, detached from the (now-finished) request ctx.
	detached := context.WithoutCancel(r.Context())
	h.svc.MeterEgress(detached, res.TenantID, n)
	h.svc.RecordAccess(detached, res.ID)
}

func (h *Handlers) PublicBrowse(w http.ResponseWriter, r *http.Request) {
	if !publicLinkLimiter.Allow(ratelimit.ClientIP(r) + "|" + r.PathValue("token")) {
		ratelimit.TooMany(w)
		return
	}
	res, err := h.svc.ByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	if err := h.svc.Authorized(res, r.URL.Query().Get("pw")); err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	var folderID int64
	if raw := r.URL.Query().Get("folder_id"); raw != "" {
		folderID, _ = strconv.ParseInt(raw, 10, 64)
	}
	entries, err := h.svc.Browse(r.Context(), res, folderID)
	if err != nil {
		writeJSON(w, statusForShareErr(err), errorResp{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resource_name": res.ResourceName,
		"entries":       entries,
	})
}

func statusForShareErr(err error) int {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrResourceNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrRevoked), errors.Is(err, ErrExpired):
		return http.StatusGone
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrPasswordRequired), errors.Is(err, ErrWrongPassword):
		return http.StatusUnauthorized
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrPasswordTooShort):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// statusForUploadErr maps files-package upload errors to HTTP statuses for a
// public portal upload.
func statusForUploadErr(err error) int {
	switch {
	case errors.Is(err, files.ErrQuotaExceeded), errors.Is(err, files.ErrWorkspaceQuotaExceeded),
		errors.Is(err, files.ErrTransferExceeded):
		return http.StatusInsufficientStorage
	case errors.Is(err, files.ErrFileTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, files.ErrInvalidName):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// friendlyUploadErr is a recipient-facing message for a portal upload failure
// (no internal "files:" prefixes leak to anonymous uploaders).
func friendlyUploadErr(err error) string {
	switch {
	case errors.Is(err, files.ErrQuotaExceeded), errors.Is(err, files.ErrWorkspaceQuotaExceeded):
		return "This drop box is full — the owner is out of storage."
	case errors.Is(err, files.ErrTransferExceeded):
		return "This drop box is temporarily unavailable. Please try again later."
	case errors.Is(err, files.ErrFileTooLarge):
		return "That file is too large."
	case errors.Is(err, files.ErrInvalidName):
		return "That file name isn't allowed."
	default:
		return "Upload failed. Please try again."
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// sanitizeFilename strips characters that break a Content-Disposition header.
func sanitizeFilename(name string) string {
	repl := func(r rune) rune {
		if r == '"' || r == '\\' || r < 0x20 {
			return '_'
		}
		return r
	}
	out := make([]rune, 0, len(name))
	for _, r := range name {
		out = append(out, repl(r))
	}
	return string(out)
}
