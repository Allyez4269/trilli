// Package officeapi backs the Trilli Productivity editor's host-side actions.
//
// Phase 2: the editor's live document lives in a per-session working blob in
// the tenant's own encrypted blob store (see system/officesessions), NOT on the
// app server's local disk. This package now:
//
//   - mints edit sessions (POST /api/office/session) — returns the WOPISrc +
//     access token the React host loads the engine with,
//   - serves the WOPI endpoints the Collabora engine calls server-side
//     (CheckFileInfo / GetFile / PutFile) over the per-session working blob,
//     reading/writing it through the encryption-wrapped store so the engine
//     only ever sees plaintext while the bytes at rest stay encrypted,
//   - Save As / Download export from the working blob (native format streamed
//     straight; legacy formats via headless LibreOffice).
//
// trilli-serve holds the per-tenant decryption key; the engine never touches
// ciphertext. The WOPI endpoints are loopback-only (the engine reaches
// 127.0.0.1) and gated by the unguessable session key (the access_token).
package officeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"trilli/system/auth"
	"trilli/system/files"
	"trilli/system/logging"
	"trilli/system/officesessions"
)

const packageName = "officeapi"

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Converter renders a source document into a different Office format (e.g.
// docx→doc) via headless LibreOffice. nil means conversion is unavailable —
// callers fall back to the app's native format only. Implemented structurally
// by docpreview.LibreOfficeConverter.
type Converter interface {
	Convert(ctx context.Context, src io.Reader, srcExt, targetExt string) ([]byte, error)
}

type Handlers struct {
	sessions  *officesessions.Service
	files     *files.Service
	converter Converter // optional; nil = native-format only
}

func NewHandlers(sess *officesessions.Service, filesSvc *files.Service, conv Converter) *Handlers {
	return &Handlers{sessions: sess, files: filesSvc, converter: conv}
}

// Register wires the app-facing office endpoints (session-gated) onto the main
// mux, and the WOPI endpoints (token-gated, loopback-only) onto the same mux —
// the engine reaches them via 127.0.0.1 through nginx/the local listener.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("POST /api/office/session", requireAuth(http.HandlerFunc(h.CreateSession)))
	m.Handle("GET /api/office/session/active", requireAuth(http.HandlerFunc(h.GetActiveSession)))
	m.Handle("GET /api/office/session/size", requireAuth(http.HandlerFunc(h.GetSessionSize)))
	m.Handle("GET /api/office/session/status", requireAuth(http.HandlerFunc(h.GetSessionStatus)))
	m.Handle("DELETE /api/office/sessions", requireAuth(http.HandlerFunc(h.EndAllSessions)))
	m.Handle("POST /api/office/save-as", requireAuth(http.HandlerFunc(h.SaveAs)))
	m.Handle("POST /api/office/open", requireAuth(http.HandlerFunc(h.Open)))
	m.Handle("POST /api/office/new", requireAuth(http.HandlerFunc(h.New)))
	m.Handle("GET /api/office/download", requireAuth(http.HandlerFunc(h.Download)))
	m.Handle("GET /api/office/print", requireAuth(http.HandlerFunc(h.Print)))
	// Insert Image asset upload (host → trilli-serve) + fetch (engine → trilli-serve).
	// The engine's insertfile fetches the image URL server-side, so the host
	// uploads the picked image here and hands the returned URL to
	// Action_InsertGraphic. The fetch endpoint is session-key-gated (the URL is
	// unguessable) and loopback-only like the other WOPI routes.
	m.Handle("POST /api/office/asset", requireAuth(http.HandlerFunc(h.UploadAsset)))
	m.Handle("GET /api/office/wopi/asset/{key}/{path...}", http.HandlerFunc(h.WOPIGetAsset))
	// WOPI endpoints — the engine calls these server-side with the session key
	// as access_token. They are NOT behind RequireAuth (there's no app session
	// cookie); the session key IS the credential, and they're only reachable on
	// the loopback WOPI listener.
	m.Handle("GET /api/office/wopi/files/{key}", http.HandlerFunc(h.WOPICheckFileInfo))
	m.Handle("GET /api/office/wopi/files/{key}/contents", http.HandlerFunc(h.WOPIGetFile))
	m.Handle("POST /api/office/wopi/files/{key}/contents", http.HandlerFunc(h.WOPIPutFile))
}

// formatDef is one export format for an app: the file extension (no dot) + the
// MIME type to store/serve it as.
type formatDef struct {
	ext   string // lowercase, no leading dot — "docx", "doc"
	ctype string
}

// appFormats is the export-format menu per app (formats[0] = the app's native
// format, which needs no conversion).
var appFormats = map[string][]formatDef{
	"docs": {
		{ext: "docx", ctype: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{ext: "doc", ctype: "application/msword"},
	},
	"sheets": {
		{ext: "xlsx", ctype: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{ext: "xls", ctype: "application/vnd.ms-excel"},
	},
	"slides": {
		{ext: "pptx", ctype: "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{ext: "ppt", ctype: "application/vnd.ms-powerpoint"},
	},
}

// resolveFormat finds the requested format for the app. The empty string means
// "use the native format" (formats[0]).
func resolveFormat(app, format string) (formatDef, bool) {
	formats, ok := appFormats[app]
	if !ok {
		return formatDef{}, false
	}
	format = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(format, ".")))
	if format == "" {
		return formats[0], true
	}
	for _, f := range formats {
		if f.ext == format {
			return f, true
		}
	}
	return formatDef{}, false
}

// nativeExt returns the app's native extension (no dot).
func nativeExt(app string) string {
	if formats, ok := appFormats[app]; ok && len(formats) > 0 {
		return formats[0].ext
	}
	return ""
}

type errorResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// canReadFile reports whether the identity may read the given file, enforcing
// the SAME per-folder ACL the web file handlers apply (files.Service.Download is
// tenant-scoped only). The Office endpoints take a file_id from the client, so
// this gate is what stops a folder-restricted member from reaching a file
// outside their granted folders. A missing/trashed file resolves to no-access
// so existence isn't leaked.
func (h *Handlers) canReadFile(ctx context.Context, identity *auth.Identity, fileID int64) bool {
	_, folderID, err := h.files.FileMeta(ctx, identity.Tenant.ID, fileID)
	if err != nil {
		return false
	}
	return identity.Access.CanRead(folderID)
}

// canWriteFileFolder reports whether the identity may write into the folder that
// CURRENTLY holds the given file — used to authorize an in-place overwrite
// against the TARGET file's own location, never a client-supplied folder.
func (h *Handlers) canWriteFileFolder(ctx context.Context, identity *auth.Identity, fileID int64) (bool, error) {
	_, folderID, err := h.files.FileMeta(ctx, identity.Tenant.ID, fileID)
	if err != nil {
		return false, err
	}
	return identity.Access.CanWrite(folderID), nil
}

// CreateSession mints an edit session and seeds its working blob. Body:
//
//	{"app": "docs", "file_id": 123}   — open an existing Trilli file (Edit/Open)
//	{"app": "docs"}                    — start a blank New document
//
// Returns the WOPISrc + access token the React host uses to load the engine.
// The prior session for this user+app (if any) is ended first, so only ONE
// working file per user+app lives at a time — the sanitation guarantee for
// New/Open replacing the current doc.
func (h *Handlers) CreateSession(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}

	var req struct {
		App      string `json:"app"`
		FileID   *int64 `json:"file_id"`
		FileName string `json:"file_name"` // optional display name override
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request"})
		return
	}
	if _, ok := officesessions.Apps[req.App]; !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "unknown app"})
		return
	}

	// COLLABORATIVE JOIN: if another participant is already editing this exact
	// file, attach to their session (same WOPISrc -> Collabora merges the views:
	// shared document, live cursors, name tags) instead of forking a private
	// working copy. Checked BEFORE the end-prior sweep so re-opening your own
	// active doc rejoins it rather than destroying it.
	if req.FileID != nil {
		if !h.canReadFile(r.Context(), identity, *req.FileID) {
			writeJSON(w, http.StatusForbidden, errorResp{Error: "You don't have access to this file"})
			return
		}
		if shared, err := h.sessions.FindActiveByFile(r.Context(), identity.Tenant.ID, *req.FileID, req.App); err == nil && shared != nil {
			tok, terr := h.sessions.MintToken(r.Context(), shared.SessionKey, identity.User.ID)
			if terr == nil {
				h.sessions.Touch(r.Context(), shared.SessionKey, shared.FileName)
				wopiSrc := fmt.Sprintf("%s/api/office/wopi/files/%s", wopiHost(), shared.SessionKey)
				resp := map[string]any{
					"session_key":  shared.SessionKey,
					"wopi_src":     wopiSrc,
					"access_token": tok,
					"file_name":    shared.FileName,
					"joined":       true,
				}
				if shared.SourceFileID != nil {
					resp["source_file_id"] = *shared.SourceFileID
				}
				writeJSON(w, http.StatusOK, resp)
				return
			}
			logging.Error(packageName, "CreateSession: join mint: %v", terr)
		}
	}

	// End the user's prior session for this app first — New/Open discards the
	// previous working file so the scratch area never accumulates stale docs.
	if _, err := h.sessions.EndUserApp(r.Context(), identity.User.ID, req.App); err != nil {
		logging.Error(packageName, "CreateSession: end prior: %v", err)
		// non-fatal — proceed to mint the new session
	}

	in := officesessions.CreateInput{
		TenantID: identity.Tenant.ID,
		UserID:   identity.User.ID,
		App:      req.App,
		FileName: req.FileName,
	}

	if req.FileID != nil {
		// Open/Edit: seed the working blob from a real Trilli file. (The
		// per-folder read gate already ran in the collaborative-join check.)
		file, rc, err := h.files.Download(r.Context(), identity.Tenant.ID, *req.FileID)
		if err != nil {
			logging.Info(packageName, "CreateSession: download file=%d: %v", *req.FileID, err)
			writeJSON(w, http.StatusNotFound, errorResp{Error: "file not found"})
			return
		}
		in.FileName = firstNonEmpty(req.FileName, file.Name)
		in.Source = &officesessions.Source{FileID: *req.FileID, Reader: rc}
		// Create closes the reader; defer is a safety net only.
		defer rc.Close()
	}

	sess, err := h.sessions.Create(r.Context(), in)
	if err != nil {
		logging.Error(packageName, "CreateSession: create: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "couldn't start session"})
		return
	}

	// The WOPISrc the engine resolves server-side. The host (WOPIHost) is the
	// loopback trilli-serve WOPI listener; the browser never contacts it.
	wopiSrc := fmt.Sprintf("%s/api/office/wopi/files/%s", wopiHost(), sess.SessionKey)
	creatorTok, terr := h.sessions.MintToken(r.Context(), sess.SessionKey, identity.User.ID)
	if terr != nil {
		creatorTok = sess.SessionKey // degrade to legacy key auth
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_key":  sess.SessionKey,
		"wopi_src":     wopiSrc,
		"access_token": creatorTok,
		"file_name":    sess.FileName,
	})
}

// GetActiveSession returns the user's active (non-expired) session for an app,
// so the editor can RESUME it on refresh instead of creating a new one. This
// preserves the auto-saved working blob (the engine writes to it every ~30s via
// WOPI PutFile), so edits + inserted images survive a refresh. Returns 404 when
// no active session exists (first visit, or the janitor reaped it) — the SPA
// then creates a new blank session via POST /api/office/session.
func (h *Handlers) GetActiveSession(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	app := strings.TrimSpace(r.URL.Query().Get("app"))
	if _, ok := officesessions.Apps[app]; !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "unknown app"})
		return
	}
	sess, err := h.sessions.GetActiveUserApp(r.Context(), identity.User.ID, app)
	if err != nil {
		// Not a creator — maybe a JOINER of someone else's shared session.
		sess, err = h.sessions.GetActiveJoined(r.Context(), identity.User.ID, app)
	}
	if err != nil || sess == nil {
		writeJSON(w, http.StatusNotFound, errorResp{Error: "no active session"})
		return
	}
	// Renew the TTL so a resumed session stays alive.
	h.sessions.Touch(r.Context(), sess.SessionKey, sess.FileName)
	tok, terr := h.sessions.MintToken(r.Context(), sess.SessionKey, identity.User.ID)
	if terr != nil {
		tok = sess.SessionKey
	}
	wopiSrc := fmt.Sprintf("%s/api/office/wopi/files/%s", wopiHost(), sess.SessionKey)
	resp := map[string]any{
		"session_key":  sess.SessionKey,
		"wopi_src":     wopiSrc,
		"access_token": tok,
		"file_name":    sess.FileName,
	}
	// Include the source file ID so the client knows whether the doc was
	// previously saved (and can silently overwrite on subsequent Saves instead
	// of opening the Save As modal).
	if sess.SourceFileID != nil {
		resp["source_file_id"] = *sess.SourceFileID
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetSessionStatus reports whether the session's SOURCE file still exists —
// so a co-editor whose file was deleted by someone else learns of it (the
// editor polls this and surfaces a banner). Participant-gated.
func (h *Handlers) GetSessionStatus(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	sess, err := h.sessions.Get(r.Context(), key)
	if err != nil || sess == nil || sess.TenantID != identity.Tenant.ID ||
		!h.sessions.IsParticipant(r.Context(), key, identity.User.ID) {
		writeJSON(w, http.StatusNotFound, errorResp{Error: "not found"})
		return
	}
	sourceDeleted := false
	if sess.SourceFileID != nil {
		st, serr := h.files.FileStatus(r.Context(), identity.Tenant.ID, *sess.SourceFileID)
		// gone entirely, or trashed => the source no longer exists as a live file
		if serr != nil || st != "active" {
			sourceDeleted = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"source_deleted": sourceDeleted})
}

// GetSessionSize returns the current size_bytes of a session's working blob.
// Used by the editor's save() to POLL until the engine's WOPI PutFile lands
// (the size changes), ensuring the host reads fresh bytes when it saves to Trilli.
func (h *Handlers) GetSessionSize(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "missing key"})
		return
	}
	sess, err := h.sessions.Get(r.Context(), key)
	// Any PARTICIPANT (creator or joiner) may poll — a joiner's session is owned
	// by the creator, so a user-id equality check would 404 their save handshake.
	if err != nil || sess == nil || sess.TenantID != identity.Tenant.ID ||
		!h.sessions.IsParticipant(r.Context(), key, identity.User.ID) {
		writeJSON(w, http.StatusNotFound, errorResp{Error: "not found"})
		return
	}
	// put_seq is the authoritative flush signal the client's save() polls on
	// (size_bytes alone misses a same-length re-save). size_bytes stays for
	// backward compatibility / diagnostics.
	writeJSON(w, http.StatusOK, map[string]any{"size_bytes": sess.SizeBytes, "put_seq": sess.PutSeq})
}

// wopiHost is the loopback origin the Collabora engine reaches trilli-serve's
// WOPI endpoints on. trilli-serve runs a SECOND plain-HTTP listener bound to
// loopback for exactly this (the engine can't speak TLS to the public :8081
// listener, and the browser never contacts this host). Overridable via
// TRILLI_WOPI_HOST for non-default deployments.
func wopiHost() string {
	if h := strings.TrimSpace(os.Getenv("TRILLI_WOPI_HOST")); h != "" {
		return h
	}
	return "http://127.0.0.1:8090"
}

// EndAllSessions ends ALL of the caller's office sessions — used on logout so
// the scratch area is wiped and the next login starts fresh, not resuming a
// stale working blob the user abandoned without saving.
func (h *Handlers) EndAllSessions(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	n, err := h.sessions.EndAllForUser(r.Context(), identity.User.ID)
	if err != nil {
		logging.Error(packageName, "EndAllSessions: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ended": n})
}

// assetHost is the origin the engine's kit (jailed) process uses to FETCH an
// inserted image. Unlike CheckFileInfo/GetFile (handled by the coolwsd parent,
// which resolves the internal WOPI domain), the engine's insertfile fetch runs
// inside the per-document kit JAIL — which doesn't inherit the host's
// /etc/hosts and so CAN'T resolve wopi.trilli.internal. The jail CAN reach
// loopback, so the asset URL uses 127.0.0.1 by default. Overridable via
// TRILLI_ASSET_HOST for non-default deployments.
func assetHost() string {
	if h := strings.TrimSpace(os.Getenv("TRILLI_ASSET_HOST")); h != "" {
		return h
	}
	return "http://127.0.0.1:8090"
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(getenv(k)); v != "" {
		return v
	}
	return def
}

// getenv is pulled out so tests can stub it; the production call uses os.Getenv.
var getenv = func(k string) string { return os.Getenv(k) }

// ----- WOPI endpoints (engine → trilli-serve, loopback) ---------------------

// wopiAuth resolves the session credential and loads the session. The engine
// passes the session key as the access_token — sometimes in the query string
// (CheckFileInfo) and sometimes in the Authorization: Bearer header
// (GetFile/PutFile, where the query access_token is empty). Accept BOTH so the
// engine's WOPI fetches authenticate regardless of where it puts the token.
// The session key is the ONLY credential the engine sees.
func (h *Handlers) wopiAuth(r *http.Request) *officesessions.Session {
	sess, _ := h.wopiAuthUser(r)
	return sess
}

// wopiAuthUser additionally resolves WHICH participant the token belongs to —
// collaborative sessions carry one token per user so CheckFileInfo can hand
// Collabora a distinct identity (name tag, cursor color) per person.
func (h *Handlers) wopiAuthUser(r *http.Request) (*officesessions.Session, int64) {
	token := strings.TrimSpace(r.URL.Query().Get("access_token"))
	if token == "" {
		// WOPI also sends the token as Authorization: Bearer <token>.
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		}
	}
	if token == "" {
		return nil, 0
	}
	sess, actor, err := h.sessions.ResolveToken(r.Context(), token)
	if err != nil {
		return nil, 0
	}
	return sess, actor
}

// WOPICheckFileInfo — GET /wopi/files/{key}?access_token={key}
// Returns the document metadata the engine needs to open the working blob.
func (h *Handlers) WOPICheckFileInfo(w http.ResponseWriter, r *http.Request) {
	sess, actor := h.wopiAuthUser(r)
	logging.Info(packageName, "WOPI CheckFileInfo key=%s hit=%v size=%d", r.URL.Query().Get("access_token"), sess != nil, func() int64 {
		if sess != nil {
			return sess.SizeBytes
		}
		return 0
	}())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Touch the idle TTL on every engine access so an active edit session never
	// expires out from under the user.
	h.sessions.Touch(r.Context(), sess.SessionKey, sess.FileName)

	// Size is load-bearing: the engine SKIPS GetFile when CheckFileInfo reports
	// Size 0, rendering a blank canvas. It's tracked on the session row (seeded
	// on Create, updated on every PutFile) so we don't have to stat the blob on
	// every engine poll.
	// Build the user-friendly name from the session's user_id. The WOPI handler
	// doesn't have the app identity (it's session-key auth, not cookie auth),
	// so we look up the user's name from the DB.
	if actor == 0 {
		actor = sess.UserID
	}
	userFriendly := h.sessions.UserFriendlyName(r.Context(), actor)
	info := map[string]any{
		"BaseFileName":     fileNameForWPI(sess),
		"Size":             sess.SizeBytes,
		"OwnerId":          fmt.Sprintf("trilli-%d", sess.TenantID),
		"UserId":           fmt.Sprintf("trilli-%d", actor),
		"UserFriendlyName": userFriendly,
		"UserCanWrite":     true,
		// WOPI Version must be STABLE while the stored file is unchanged and only
		// advance when the working blob actually changes — i.e. on a real PutFile.
		// put_seq is exactly that counter. The previous time.Now() value changed on
		// every CheckFileInfo poll, signalling a spurious external-storage change to
		// Collabora on every tick (incorrect WOPI semantics; risks reload/conflict
		// churn mid-edit). Seeded sessions start at put_seq=0, which still differs
		// from any prior version the engine cached, so initial GetFile still fires.
		"Version":           fmt.Sprintf("v%d", sess.PutSeq),
		"PostMessageOrigin": postMessageOrigin(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// WOPIGetFile — GET /wopi/files/{key}/contents?access_token={key}
// Streams the decrypted working blob to the engine.
func (h *Handlers) WOPIGetFile(w http.ResponseWriter, r *http.Request) {
	sess := h.wopiAuth(r)
	logging.Info(packageName, "WOPI GetFile key=%s hit=%v", r.URL.Query().Get("access_token"), sess != nil)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.sessions.Touch(r.Context(), sess.SessionKey, sess.FileName)
	rc, err := h.sessions.GetWorking(r.Context(), sess)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.Copy(w, rc); err != nil {
		logging.Error(packageName, "WOPIGetFile: copy: %v", err)
	}
}

// WOPIPutFile — POST /wopi/files/{key}/contents?access_token={key}
// Writes the engine's latest bytes into the (re-encrypted) working blob.
func (h *Handlers) WOPIPutFile(w http.ResponseWriter, r *http.Request) {
	sess := h.wopiAuth(r)
	if sess == nil {
		// Expected during teardown: a late idle-save PutFile can arrive just after
		// the session was ended (New/Open/logout). Log at Debug, not Info — it's
		// not an error, and the scratch working blob is gone by design.
		logging.Debug(packageName, "WOPI PutFile for unknown/ended session key=%s", r.URL.Query().Get("access_token"))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	logging.Info(packageName, "WOPI PutFile key=%s hit=true", r.URL.Query().Get("access_token"))
	if _, err := h.sessions.PutWorking(r.Context(), sess, r.Body); err != nil {
		logging.Error(packageName, "WOPIPutFile: %v", err)
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}
	h.sessions.Touch(r.Context(), sess.SessionKey, sess.FileName)
	// Return the new item version (PutWorking bumped put_seq) so the engine knows
	// the version THIS save produced — matching WOPICheckFileInfo's "v{put_seq}".
	// Without it the engine learns the bumped version only on the next poll and
	// treats its OWN save as an external storage change, re-fetching needlessly.
	w.Header().Set("X-WOPI-ItemVersion", fmt.Sprintf("v%d", sess.PutSeq))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"LastModifiedTime": time.Now().UTC().Format(time.RFC3339)})
}

// UploadAsset receives an image the host picked (Insert Image) and stores it in
// the session's hidden working area. Returns the server-fetchable URL the host
// hands to Action_InsertGraphic — the engine fetches it server-side (LOKit's
// insertfile can't read a data: URL, which is why the old data-URL path didn't
// insert). The URL carries the unguessable session key + asset id, so the fetch
// endpoint needs no separate auth.
func (h *Handlers) UploadAsset(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MiB image cap
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "image too large or invalid"})
		return
	}
	sessionKey := strings.TrimSpace(r.FormValue("session_key"))
	sess, err := h.sessions.Get(r.Context(), sessionKey)
	if err != nil || sess.TenantID != identity.Tenant.ID {
		writeJSON(w, http.StatusNotFound, errorResp{Error: "session not found"})
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "missing file"})
		return
	}
	defer file.Close()
	if ct := hdr.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "file must be an image"})
		return
	}
	path, err := h.sessions.PutAsset(r.Context(), sess, file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "couldn't store image"})
		return
	}
	url := fmt.Sprintf("%s/api/office/wopi/asset/%s/%s", assetHost(), sess.SessionKey, path)
	logging.Info(packageName, "UploadAsset: key=%s -> %s", sess.SessionKey, url)
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// WOPIGetAsset serves a session asset to the engine (its server-side insertfile
// fetch). Gated by the session key in the path (unguessable) + the asset path
// being scoped under that session.
func (h *Handlers) WOPIGetAsset(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	assetPath := r.PathValue("path")
	logging.Info(packageName, "WOPIGetAsset: key=%s path=%s", key, assetPath)
	sess, err := h.sessions.Get(r.Context(), key)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rc, err := h.sessions.GetAsset(r.Context(), sess, assetPath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, max-age=300")
	if _, err := io.Copy(w, rc); err != nil {
		_ = err
	}
}

func fileNameForWPI(sess *officesessions.Session) string {
	if sess.FileName == "" {
		return "document." + nativeExt(sess.App)
	}
	if !hasOfficeExt(sess.FileName) {
		return withExt(sess.FileName, nativeExt(sess.App))
	}
	return sess.FileName
}

// postMessageOrigin is the embedding app's origin — Collabora only emits the
// postMessages (load/save state, Hide_Menubar, etc.) the React host relies on
// when CheckFileInfo returns it. The SPA is served from app.trilli.com.
func postMessageOrigin() string {
	if v := strings.TrimSpace(envOr("TRILLI_POST_MESSAGE_ORIGIN", "")); v != "" {
		return v
	}
	return "https://app.trilli.com"
}

// ----- Save As / Download / Open / New -------------------------------------

type saveAsReq struct {
	SessionKey      string `json:"session_key"`
	Name            string `json:"name"`
	Format          string `json:"format"`
	WorkspaceID     *int64 `json:"workspace_id"`
	FolderID        *int64 `json:"folder_id"`
	OverwriteFileID *int64 `json:"overwrite_file_id"` // when set, replace this file in-place instead of creating a new one
}

// SaveAs commits the working blob into a real Trilli file (encrypted, in the
// chosen workspace/folder), exported into the requested format. The React host
// flushes the engine's latest bytes (editor.save() → WOPI PutFile) first, so
// the saved file reflects the user's edits.
func (h *Handlers) SaveAs(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}

	var req saveAsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request"})
		return
	}
	sess, err := h.sessions.Get(r.Context(), req.SessionKey)
	if err != nil || sess.TenantID != identity.Tenant.ID {
		writeJSON(w, http.StatusNotFound, errorResp{Error: "session not found"})
		return
	}
	fmtDef, okFmt := resolveFormat(sess.App, req.Format)
	if !okFmt {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "unsupported format"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = sess.FileName
	}
	if name == "" {
		name = "document"
	}
	name = withExt(name, fmtDef.ext)

	// Folder-access gating. The write target differs by mode: a NEW Save As
	// writes into req.FolderID (which the caller must be able to write), while an
	// in-place OVERWRITE writes into the TARGET file's existing folder — so we
	// authorize THAT folder, not the client-supplied one. Authorizing the request
	// body's folder for an overwrite would let a folder-restricted member replace
	// any file in the tenant by pointing overwrite_file_id at it.
	overwrite := req.OverwriteFileID != nil && *req.OverwriteFileID > 0
	resavedNew := false
	if overwrite {
		// If the target was deleted/trashed (e.g. by a collaborator), do NOT
		// resurrect it — fall through to a fresh Save As so the user's work is
		// preserved as a new file, and flag it so the client can tell them.
		if st, serr := h.files.FileStatus(r.Context(), identity.Tenant.ID, *req.OverwriteFileID); serr != nil || st != "active" {
			overwrite = false
			resavedNew = true
		}
	}
	if overwrite {
		can, ferr := h.canWriteFileFolder(r.Context(), identity, *req.OverwriteFileID)
		if ferr != nil {
			writeJSON(w, http.StatusNotFound, errorResp{Error: "file not found"})
			return
		}
		if !can {
			writeJSON(w, http.StatusForbidden, errorResp{Error: "forbidden"})
			return
		}
	} else if !identity.Access.CanWrite(req.FolderID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: "forbidden"})
		return
	}

	// No server-side flush wait here: the client awaits editorRef.save() — which
	// now blocks until the engine's WOPI PutFile has landed (it polls the
	// session's put_seq, the monotonic flush counter, not a size change) — BEFORE
	// calling this endpoint. So the working blob already holds the user's latest
	// edits, and exportWorking reads them directly. (The old size-change poll
	// missed same-length re-saves, which is what made subsequent saves capture
	// stale bytes; Download/Print rely on the same client flush and have no poll.)
	body, ctype, err := h.exportWorking(r.Context(), sess, fmtDef)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: err.Error()})
		return
	}
	defer body.Close()

	var file *files.File
	if overwrite {
		// Overwrite: replace the existing file's blob + metadata in-place.
		file, err = h.files.OverwriteFile(r.Context(), identity.Tenant.ID, *req.OverwriteFileID, name, ctype, body, identity.User.ID)
	} else {
		// Normal Save As: create a new file.
		file, err = h.files.Upload(r.Context(), files.UploadInput{
			TenantID:       identity.Tenant.ID,
			UploaderID:     identity.User.ID,
			Name:           name,
			ContentType:    ctype,
			Reader:         body,
			ParentFolderID: req.FolderID,
			WorkspaceID:    req.WorkspaceID,
		})
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: err.Error()})
		return
	}

	_ = h.sessions.SetFileName(r.Context(), sess.SessionKey, file.Name)
	if file.ID > 0 {
		_ = h.sessions.SetSourceFileID(r.Context(), sess.SessionKey, file.ID)
	}
	if resavedNew {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": file.ID, "name": file.Name, "resaved_new": true,
		})
		return
	}
	writeJSON(w, http.StatusOK, file)
}

// Print converts the working blob to PDF via headless LibreOffice and serves
// it INLINE — the hidden-iframe print flow opens this URL, and the browser's
// native print dialog prints the PDF. Works from the LIVE working blob (the
// auto-saved scratch), so the user can print without an explicit Save As.
// The React host flushes the engine's latest bytes (editor.save() → WOPI
// PutFile) before calling this.
func (h *Handlers) Print(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	sessionKey := strings.TrimSpace(r.URL.Query().Get("session_key"))
	sess, err := h.sessions.Get(r.Context(), sessionKey)
	if err != nil || sess.TenantID != identity.Tenant.ID {
		writeJSON(w, http.StatusNotFound, errorResp{Error: "session not found"})
		return
	}
	rc, err := h.sessions.GetWorking(r.Context(), sess)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "document not available"})
		return
	}
	defer rc.Close()
	native := nativeExt(sess.App)
	if h.converter == nil {
		// No LibreOffice — serve the native bytes (most browsers can't print
		// a .docx, but at least it doesn't error).
		w.Header().Set("Content-Type", appFormats[sess.App][0].ctype)
		w.Header().Set("Content-Disposition", "inline")
		io.Copy(w, rc)
		return
	}
	pdf, err := h.converter.Convert(r.Context(), rc, native, "pdf")
	if err != nil {
		logging.Error(packageName, "Print: convert to PDF: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "couldn't generate PDF"})
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline")
	w.Write(pdf)
}

// Download streams the working blob to the browser as an attachment, exported
// into the requested format. The React host flushes the engine first.
func (h *Handlers) Download(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}

	sessionKey := strings.TrimSpace(r.URL.Query().Get("session_key"))
	sess, err := h.sessions.Get(r.Context(), sessionKey)
	if err != nil || sess.TenantID != identity.Tenant.ID {
		writeJSON(w, http.StatusNotFound, errorResp{Error: "session not found"})
		return
	}
	fmtDef, okFmt := resolveFormat(sess.App, r.URL.Query().Get("format"))
	if !okFmt {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "unsupported format"})
		return
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = sess.FileName
	}
	if name == "" {
		name = "document"
	}
	name = withExt(name, fmtDef.ext)

	body, _, err := h.exportWorking(r.Context(), sess, fmtDef)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: err.Error()})
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", fmtDef.ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeDownloadName(name)))
	if _, err := io.Copy(w, body); err != nil {
		_ = err
	}
}

// exportWorking opens the session's working blob and returns a reader over its
// bytes in the requested format. The native format streams straight from the
// (decrypted) blob; a legacy format is produced by piping it through the
// LibreOffice converter. Callers Close the returned reader.
func (h *Handlers) exportWorking(ctx context.Context, sess *officesessions.Session, fmtDef formatDef) (io.ReadCloser, string, error) {
	native := nativeExt(sess.App)
	if fmtDef.ext == native || h.converter == nil {
		rc, err := h.sessions.GetWorking(ctx, sess)
		if err != nil {
			return nil, "", fmt.Errorf("document not available")
		}
		ct := fmtDef.ctype
		if fmtDef.ext != native {
			ct = appFormats[sess.App][0].ctype
		}
		return rc, ct, nil
	}

	// Non-native format: convert via headless LibreOffice.
	in, err := h.sessions.GetWorking(ctx, sess)
	if err != nil {
		return nil, "", fmt.Errorf("document not available")
	}
	defer in.Close()

	converted, err := h.converter.Convert(ctx, in, native, fmtDef.ext)
	if err != nil {
		return nil, "", fmt.Errorf("couldn't export to %s", fmtDef.ext)
	}
	return io.NopCloser(bytes.NewReader(converted)), fmtDef.ctype, nil
}

// Open seeds a session's working blob from an existing Trilli file. Kept for
// backward compatibility with the React "Open from Trilli" modal flow; the
// preferred path is CreateSession with file_id. Body: {session_key, file_id}.
// (The React host passes the session to re-seed; a fresh session is created if
// session_key is absent.)
func (h *Handlers) Open(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req struct {
		SessionKey string `json:"session_key"`
		App        string `json:"app"`
		FileID     int64  `json:"file_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request"})
		return
	}
	if req.App == "" {
		// Fall back to the existing session's app when omitted.
		if s, err := h.sessions.Get(r.Context(), req.SessionKey); err == nil {
			req.App = s.App
		}
	}
	if _, ok := officesessions.Apps[req.App]; !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "unknown app"})
		return
	}
	// End the prior session for this user+app, then create a fresh one seeded
	// from the file — same sanitation as CreateSession.
	// Per-folder ACL gate (files.Download is tenant-scoped only) — same as
	// CreateSession, so a folder-restricted member can't open a file outside
	// their granted folders by passing its id here.
	if !h.canReadFile(r.Context(), identity, req.FileID) {
		writeJSON(w, http.StatusForbidden, errorResp{Error: "You don't have access to this file"})
		return
	}
	if _, err := h.sessions.EndUserApp(r.Context(), identity.User.ID, req.App); err != nil {
		logging.Error(packageName, "Open: end prior: %v", err)
	}
	file, rc, err := h.files.Download(r.Context(), identity.Tenant.ID, req.FileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResp{Error: "file not found"})
		return
	}
	defer rc.Close()
	sess, err := h.sessions.Create(r.Context(), officesessions.CreateInput{
		TenantID: identity.Tenant.ID,
		UserID:   identity.User.ID,
		App:      req.App,
		FileName: file.Name,
		Source:   &officesessions.Source{FileID: req.FileID, Reader: rc},
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "couldn't open document"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_key":  sess.SessionKey,
		"wopi_src":     fmt.Sprintf("%s/api/office/wopi/files/%s", wopiHost(), sess.SessionKey),
		"access_token": sess.SessionKey,
		"name":         file.Name,
	})
}

// New starts a blank document session. Body: {app, [session_key]}. Ends the
// prior session for this user+app (sanitation) and mints a fresh one seeded
// from the blank template.
func (h *Handlers) New(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Tenant == nil || identity.User == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{Error: "unauthorized"})
		return
	}
	var req struct {
		App string `json:"app"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "invalid request"})
		return
	}
	if _, ok := officesessions.Apps[req.App]; !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{Error: "unknown app"})
		return
	}
	if _, err := h.sessions.EndUserApp(r.Context(), identity.User.ID, req.App); err != nil {
		logging.Error(packageName, "New: end prior: %v", err)
	}
	sess, err := h.sessions.Create(r.Context(), officesessions.CreateInput{
		TenantID: identity.Tenant.ID,
		UserID:   identity.User.ID,
		App:      req.App,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResp{Error: "couldn't start document"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_key":  sess.SessionKey,
		"wopi_src":     fmt.Sprintf("%s/api/office/wopi/files/%s", wopiHost(), sess.SessionKey),
		"access_token": sess.SessionKey,
		"name":         sess.FileName,
	})
}

// ----- helpers -------------------------------------------------------------

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// withExt ensures name ends with the given extension (case-insensitive),
// replacing any existing office extension so "Report.docx" + "doc" →
// "Report.doc" rather than "Report.docx.doc".
func withExt(name, ext string) string {
	base := name
	// Strip ANY source extension the editor can open (not just native office
	// ones), so opening e.g. "sales.csv" and saving as xlsx yields "sales.xlsx",
	// not "sales.csv.xlsx". Mirrors stripOfficeExt / EDITABLE_EXTS on the client.
	// (hasOfficeExt is deliberately NOT broadened — a non-native source name must
	// still pass through here to gain the native ext for the WOPI BaseFileName.)
	for _, old := range []string{
		".docx", ".doc", ".odt", ".rtf", ".txt", ".md", ".markdown", ".log", ".rst", ".asciidoc",
		".xlsx", ".xls", ".xlsm", ".csv", ".tsv", ".ods",
		".pptx", ".ppt", ".pps", ".ppsx", ".odp",
	} {
		if i := strings.LastIndex(strings.ToLower(base), old); i == len(base)-len(old) && i >= 0 {
			base = base[:i]
			break
		}
	}
	return base + "." + ext
}

// hasOfficeExt reports whether name already ends with an office extension.
func hasOfficeExt(name string) bool {
	low := strings.ToLower(name)
	for _, old := range []string{".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt"} {
		if strings.HasSuffix(low, old) {
			return true
		}
	}
	return false
}

// sanitizeDownloadName strips characters that are illegal in HTTP quoted
// filenames (quotes/backslash) so the Content-Disposition filename parses.
func sanitizeDownloadName(name string) string {
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, `\`, "")
	if name == "" {
		name = "document"
	}
	return name
}
