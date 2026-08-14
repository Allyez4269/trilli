package sign

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"trilli/system/auth"
	"trilli/system/logging"
)

type errorResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrTokenNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrNotDraft), errors.Is(err, ErrNoRecipients), errors.Is(err, ErrEmptySigner),
		errors.Is(err, ErrNotSignable), errors.Is(err, ErrAlreadySigned):
		return http.StatusConflict
	case errors.Is(err, ErrNotPDF), errors.Is(err, ErrBadInput),
		errors.Is(err, ErrMissingFields), errors.Is(err, ErrNoConsent), errors.Is(err, ErrBadSignature):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func fail(w http.ResponseWriter, err error) {
	if statusFor(err) == http.StatusInternalServerError {
		logging.Error(packageName, "%v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, statusFor(err), errorResp{Error: err.Error()})
}

func ident(w http.ResponseWriter, r *http.Request) (*auth.Identity, bool) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return nil, false
	}
	return id, true
}

func pathID(r *http.Request, key string) (int64, error) {
	return strconv.ParseInt(r.PathValue(key), 10, 64)
}

func actorName(id *auth.Identity) string {
	if id.User == nil {
		return ""
	}
	if id.User.FullName != nil && *id.User.FullName != "" {
		return *id.User.FullName
	}
	return ""
}

// baseURL derives the public origin from the request, honoring the proxy's
// X-Forwarded-* headers (mirrors auth's invite-link builder).
func baseURL(r *http.Request) string {
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + host
}

// Register wires the Trilli Sign routes. EVERY tenant-facing route is wrapped
// in RequireBeta — that is the private-beta boundary. The ceremony resolver is
// the one public exception: signers are external; the token is the access.
func (s *Service) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	gated := func(h http.HandlerFunc) http.Handler { return requireAuth(RequireBeta(h)) }

	m.Handle("GET /api/sign/status", gated(s.hStatus))
	m.Handle("POST /api/sign/verify", gated(s.hVerify))
	m.Handle("GET /api/sign/settings", gated(s.hGetSettings))
	m.Handle("PUT /api/sign/settings", gated(s.hPutSettings))
	m.Handle("POST /api/sign/envelopes", gated(s.hCreate))
	m.Handle("GET /api/sign/envelopes", gated(s.hList))
	m.Handle("GET /api/sign/envelopes/{id}", gated(s.hGet))
	m.Handle("PATCH /api/sign/envelopes/{id}", gated(s.hPatch))
	m.Handle("DELETE /api/sign/envelopes/{id}", gated(s.hDelete))
	m.Handle("GET /api/sign/envelopes/{id}/pages/{page}", gated(s.hPage))
	m.Handle("GET /api/sign/envelopes/{id}/events", gated(s.hEvents))
	m.Handle("POST /api/sign/envelopes/{id}/document", gated(s.hAttachDocument))
	m.Handle("POST /api/sign/envelopes/{id}/document/upload", gated(s.hUploadDocument))
	m.Handle("DELETE /api/sign/envelopes/{id}/document", gated(s.hRemoveDocument))
	m.Handle("POST /api/sign/envelopes/{id}/recipients", gated(s.hAddRecipient))
	m.Handle("DELETE /api/sign/envelopes/{id}/recipients/{rid}", gated(s.hDelRecipient))
	m.Handle("POST /api/sign/envelopes/{id}/fields", gated(s.hAddField))
	m.Handle("PATCH /api/sign/envelopes/{id}/fields/{fid}", gated(s.hPatchField))
	m.Handle("DELETE /api/sign/envelopes/{id}/fields/{fid}", gated(s.hDelField))
	m.Handle("POST /api/sign/envelopes/{id}/send", gated(s.hSend))
	m.Handle("POST /api/sign/envelopes/{id}/resend", gated(s.hResend))
	m.Handle("GET /api/sign/envelopes/{id}/download", gated(s.hDownload))
	m.Handle("GET /api/sign/envelopes/{id}/preview", gated(s.hPreview))

	// Public ceremony (token IS the access, like share links): resolve, view
	// pages, and complete the signing.
	m.Handle("GET /api/sign/ceremony/{token}", http.HandlerFunc(s.hCeremony))
	m.Handle("GET /api/sign/ceremony/{token}/pages/{page}", http.HandlerFunc(s.hCeremonyPage))
	m.Handle("POST /api/sign/ceremony/{token}/complete", http.HandlerFunc(s.hCeremonyComplete))
	m.Handle("GET /api/sign/ceremony/{token}/download", http.HandlerFunc(s.hCeremonyDownload))
	m.Handle("POST /api/sign/ceremony/{token}/decline", http.HandlerFunc(s.hCeremonyDecline))
	m.Handle("GET /api/sign/echo", http.HandlerFunc(s.hEcho))
	m.Handle("POST /api/sign/ceremony/{token}/fields/{fid}/attachment", http.HandlerFunc(s.hCeremonyAttach))
}

func (s *Service) hStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "phase": "private-beta"})
}

// hVerify answers "is this file an artifact we sealed?" — the caller uploads
// a PDF (raw body); we hash it and match against the digests recorded at
// execution time. Complements the embedded PKCS#7 (which any third party can
// validate offline) with Trilli's own books.
func (s *Service) hVerify(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 100<<20))
	if err != nil || len(raw) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "upload the PDF as the request body"})
		return
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	var envID int64
	var title, kind string
	var completed sql.NullTime
	err = s.pg.QueryRowContext(r.Context(), `
		SELECT id, title, completed_at,
		       CASE WHEN sealed_sha256 = $2 THEN 'sealed' ELSE 'executed' END
		  FROM sign_envelopes
		 WHERE tenant_id = $1 AND (sealed_sha256 = $2 OR executed_sha256 = $2)`,
		id.Tenant.ID, digest,
	).Scan(&envID, &title, &completed, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"verified": false, "sha256": digest})
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	out := map[string]any{
		"verified": true, "artifact": kind, "sha256": digest,
		"envelope_id": envID, "title": title,
	}
	if completed.Valid {
		out["completed_at"] = completed.Time
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) hGetSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	st, err := s.GetSettings(r.Context(), id.Tenant.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Service) hPutSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	var body struct {
		WorkspaceID *int64 `json:"workspace_id"`
		FolderID    *int64 `json:"folder_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid body"})
		return
	}
	if err := s.SetSettings(r.Context(), id.Tenant.ID, body.WorkspaceID, body.FolderID); err != nil {
		fail(w, err)
		return
	}
	st, err := s.GetSettings(r.Context(), id.Tenant.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Service) hCreate(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	var body struct {
		FileID int64 `json:"file_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var e *Envelope
	var err error
	if body.FileID == 0 {
		// No document yet — a blank draft; the user attaches the PDF in setup.
		e, err = s.CreateBlankEnvelope(r.Context(), id.Tenant.ID, id.User.ID, id.User.Email)
	} else {
		e, err = s.CreateEnvelope(r.Context(), id.Tenant.ID, id.User.ID, id.User.Email, body.FileID)
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// hUploadDocument: desktop drag-drop — raw file bytes in the body, filename
// in ?name=. Staged as a protected file in Trilli Sign/Drafts, then attached.
func (s *Service) hUploadDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "name required"})
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 100<<20))
	if err != nil || len(raw) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "empty upload"})
		return
	}
	e, err := s.AttachUploadedDocument(r.Context(), id.Tenant.ID, eid, id.User.ID, name, raw, id.User.Email)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Service) hRemoveDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	e, err := s.RemoveDocument(r.Context(), id.Tenant.ID, eid, id.User.Email)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Service) hAttachDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	var body struct {
		FileID int64 `json:"file_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FileID == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "file_id required"})
		return
	}
	e, err := s.AttachDocument(r.Context(), id.Tenant.ID, eid, body.FileID, id.User.Email)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Service) hList(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	list, err := s.ListEnvelopes(r.Context(), id.Tenant.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"envelopes": list})
}

func (s *Service) hGet(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	e, err := s.GetEnvelope(r.Context(), id.Tenant.ID, eid)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Service) hPatch(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	var body struct {
		Title    *string `json:"title"`
		Subject  *string `json:"subject"`
		Category *string `json:"category"`
		Message  *string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid body"})
		return
	}
	if err := s.UpdateEnvelope(r.Context(), id.Tenant.ID, eid, id.User.Email, body.Title, body.Subject, body.Category, body.Message); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) hDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if err := s.DeleteEnvelope(r.Context(), id.Tenant.ID, eid, id.User.Email); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) hPage(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err1 := pathID(r, "id")
	page, err2 := strconv.Atoi(r.PathValue("page"))
	if err1 != nil || err2 != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid path"})
		return
	}
	pngBytes, err := s.RenderPage(r.Context(), id.Tenant.ID, eid, page)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(pngBytes)
}

func (s *Service) hEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	evs, err := s.Events(r.Context(), id.Tenant.ID, eid)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

func (s *Service) hAddRecipient(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	var body struct {
		Name         string `json:"name"`
		Email        string `json:"email"`
		SigningOrder int    `json:"signing_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid body"})
		return
	}
	rec, err := s.AddRecipient(r.Context(), id.Tenant.ID, eid, id.User.Email, body.Name, body.Email, body.SigningOrder)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Service) hDelRecipient(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err1 := pathID(r, "id")
	rid, err2 := pathID(r, "rid")
	if err1 != nil || err2 != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid path"})
		return
	}
	if err := s.DeleteRecipient(r.Context(), id.Tenant.ID, eid, rid, id.User.Email); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) hAddField(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	var f Field
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid body"})
		return
	}
	out, err := s.AddField(r.Context(), id.Tenant.ID, eid, f)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Service) hPatchField(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err1 := pathID(r, "id")
	fid, err2 := pathID(r, "fid")
	if err1 != nil || err2 != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid path"})
		return
	}
	var body struct {
		X           *float64        `json:"x"`
		Y           *float64        `json:"y"`
		W           *float64        `json:"w"`
		H           *float64        `json:"h"`
		Page        *int            `json:"page"`
		Required    *bool           `json:"required"`
		RecipientID *int64          `json:"recipient_id"`
		Meta        json.RawMessage `json:"meta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid body"})
		return
	}
	if err := s.UpdateField(r.Context(), id.Tenant.ID, eid, fid, body.X, body.Y, body.W, body.H, body.Page, body.Required, body.RecipientID, body.Meta); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) hDelField(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err1 := pathID(r, "id")
	fid, err2 := pathID(r, "fid")
	if err1 != nil || err2 != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid path"})
		return
	}
	if err := s.DeleteField(r.Context(), id.Tenant.ID, eid, fid); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) hSend(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	if err := s.Send(r.Context(), id.Tenant.ID, eid, actorName(id), id.User.Email, baseURL(r)); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func (s *Service) hResend(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	n, err := s.Resend(r.Context(), id.Tenant.ID, eid, actorName(id), id.User.Email, baseURL(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resent": n})
}

func (s *Service) hCeremony(w http.ResponseWriter, r *http.Request) {
	info, err := s.CeremonyViewFull(r.Context(), r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Service) hCeremonyPage(w http.ResponseWriter, r *http.Request) {
	page, err := strconv.Atoi(r.PathValue("page"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid page"})
		return
	}
	pngBytes, err := s.CeremonyPage(r.Context(), r.PathValue("token"), page)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(pngBytes)
}

func (s *Service) hCeremonyComplete(w http.ResponseWriter, r *http.Request) {
	var in CompleteInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid body"})
		return
	}
	view, err := s.CompleteCeremony(r.Context(), r.PathValue("token"), auth.ClientIP(r), r.UserAgent(), in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func writePDF(w http.ResponseWriter, name string, raw []byte) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(raw)
}

func (s *Service) hDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	raw, name, err := s.DownloadExecuted(r.Context(), id.Tenant.ID, eid)
	if err != nil {
		fail(w, err)
		return
	}
	writePDF(w, name, raw)
}

func (s *Service) hPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := ident(w, r)
	if !ok {
		return
	}
	eid, err := pathID(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid id"})
		return
	}
	var rid int64
	if v := r.URL.Query().Get("recipient"); v != "" {
		rid, _ = strconv.ParseInt(v, 10, 64)
	}
	view, err := s.PreviewCeremony(r.Context(), id.Tenant.ID, eid, rid)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// hEcho reflects the caller's own connection details (IP, agent, location) for
// the signing disclosure. Public by design: it reveals nothing but what the
// caller already sent us. Location resolves through our own GeoIP service
// (system/qserve) — no third-party headers.
func (s *Service) hEcho(w http.ResponseWriter, r *http.Request) {
	ip := auth.ClientIP(r)
	location := ""
	if s.geo != nil {
		if g, err := s.geo.LookupIP(ip); err == nil && g != nil {
			parts := []string{}
			if g.City != "" {
				parts = append(parts, g.City)
			}
			if len(g.Subdivisions) > 0 && g.Subdivisions[0].Name != "" {
				parts = append(parts, g.Subdivisions[0].Name)
			}
			if g.CountryName != "" {
				parts = append(parts, g.CountryName)
			}
			location = strings.Join(parts, ", ")
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"ip":         ip,
		"user_agent": r.UserAgent(),
		"location":   location,
	})
}

func (s *Service) hCeremonyDecline(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	if err := s.DeclineCeremony(r.Context(), r.PathValue("token"), auth.ClientIP(r), body.Reason); err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "declined"})
}

func (s *Service) hCeremonyAttach(w http.ResponseWriter, r *http.Request) {
	fid, err := strconv.ParseInt(r.PathValue("fid"), 10, 64)
	if err != nil {
		http.Error(w, "bad field id", http.StatusBadRequest)
		return
	}
	if err := r.ParseMultipartForm(maxAttachment + (1 << 20)); err != nil {
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	if err := s.AttachFile(r.Context(), r.PathValue("token"), fid, hdr.Filename, file); err != nil {
		http.Error(w, err.Error(), statusFor(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"filename": hdr.Filename})
}

func (s *Service) hCeremonyDownload(w http.ResponseWriter, r *http.Request) {
	raw, name, err := s.DownloadExecutedByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	writePDF(w, name, raw)
}
