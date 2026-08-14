package clouddrive

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"trilli/system/auth"
	"trilli/system/credentials"
	"trilli/system/crypto"
	"trilli/system/database/postgres"
	"trilli/system/files"
	"trilli/system/logging"
)

const packageName = "clouddrive"

const (
	defaultRedirect = "https://app.trilli.com/api/integrations/google/callback"
	stateCookie     = "g_drive_oauth_state"
	appOrigin       = "https://app.trilli.com"
	maxImportFiles  = 100
	maxFileBytes    = 512 << 20 // 512 MB per file
)

// Service wires the Cloud Import (Google Drive) handlers to the credentials vault
// (client id/secret), the per-user connection store, and the files service.
type Service struct {
	creds    *credentials.Service
	store    *store
	files    *files.Service
	redirect string
}

func NewService(db *postgres.Client, creds *credentials.Service, filesSvc *files.Service, redirect string) *Service {
	if strings.TrimSpace(redirect) == "" {
		redirect = defaultRedirect
	}
	return &Service{creds: creds, store: &store{db: db}, files: filesSvc, redirect: redirect}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the routes. All require a session except the OAuth callback,
// which Google reaches by top-level redirect and which trusts the encrypted,
// cookie-bound state for the user/tenant identity.
func (s *Service) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	m.Handle("GET /api/integrations/google/status", requireAuth(http.HandlerFunc(s.Status)))
	m.Handle("GET /api/integrations/google/auth", requireAuth(http.HandlerFunc(s.Start)))
	m.Handle("GET /api/integrations/google/callback", http.HandlerFunc(s.Callback))
	m.Handle("GET /api/integrations/google/picker-token", requireAuth(http.HandlerFunc(s.PickerToken)))
	m.Handle("POST /api/integrations/google/import", requireAuth(http.HandlerFunc(s.Import)))
	m.Handle("POST /api/integrations/google/disconnect", requireAuth(http.HandlerFunc(s.Disconnect)))
}

func (s *Service) clientCreds(ctx context.Context) (id, secret string, err error) {
	id, _, err = s.creds.GetActive(ctx, providerGoogle, "client_id")
	if err != nil {
		return "", "", fmt.Errorf("clouddrive: client id: %w", err)
	}
	secret, _, err = s.creds.GetActive(ctx, providerGoogle, "client_secret")
	if err != nil {
		return "", "", fmt.Errorf("clouddrive: client secret: %w", err)
	}
	return id, secret, nil
}

// accessToken resolves the user's stored refresh token into a fresh access token.
func (s *Service) accessToken(ctx context.Context, userID int64) (string, error) {
	conn, err := s.store.get(ctx, userID)
	if err != nil {
		return "", err
	}
	clientID, clientSecret, err := s.clientCreds(ctx)
	if err != nil {
		return "", err
	}
	return refreshAccess(ctx, clientID, clientSecret, conn.RefreshToken)
}

// ----- OAuth state (encrypted, cookie-bound) --------------------------------

type oauthState struct {
	Nonce    string `json:"n"`
	UserID   int64  `json:"u"`
	TenantID int64  `json:"t"`
	Origin   string `json:"o"`
}

func encodeState(st oauthState) (string, error) {
	b, _ := json.Marshal(st)
	enc, err := crypto.Encrypt(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(enc), nil
}

func decodeState(s string) (oauthState, error) {
	var st oauthState
	raw, err := hex.DecodeString(s)
	if err != nil {
		return st, err
	}
	plain, err := crypto.Decrypt(raw)
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(plain, &st)
	return st, err
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}

// ----- handlers -------------------------------------------------------------

// Status reports whether the caller has a live Drive connection.
func (s *Service) Status(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.User == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	conn, err := s.store.get(r.Context(), id.User.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "email": conn.AccountEmail})
}

// Start redirects to Google's consent screen, requesting offline Drive read access.
func (s *Service) Start(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.User == nil || id.Tenant == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	clientID, _, err := s.clientCreds(r.Context())
	if err != nil {
		logging.Error(packageName, "Start: creds: %v", err)
		http.Error(w, "Google Drive import is not configured.", http.StatusServiceUnavailable)
		return
	}
	nonce := randHex(16)
	st := oauthState{Nonce: nonce, UserID: id.User.ID, TenantID: id.Tenant.ID, Origin: allowedOrigin(r.URL.Query().Get("origin"))}
	enc, err := encodeState(st)
	if err != nil {
		http.Error(w, "could not start", http.StatusInternalServerError)
		return
	}
	// Lax so the cookie survives the top-level redirect back from Google.
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: nonce, Path: "/", MaxAge: 600,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", s.redirect)
	q.Set("response_type", "code")
	q.Set("scope", "openid email "+driveScope)
	q.Set("state", enc)
	q.Set("access_type", "offline") // ask for a refresh token
	q.Set("prompt", "consent")      // force it even if previously granted
	http.Redirect(w, r, googleAuthURL+"?"+q.Encode(), http.StatusSeeOther)
}

// Callback handles Google's redirect: verify state, exchange the code, store the
// encrypted refresh token, and report back through the popup channel.
func (s *Service) Callback(w http.ResponseWriter, r *http.Request) {
	st, err := decodeState(r.URL.Query().Get("state"))
	cookie, cErr := r.Cookie(stateCookie)
	clearStateCookie(w)
	if err != nil || cErr != nil || st.Nonce == "" || st.Nonce != cookie.Value {
		popupResult(w, st.Origin, "error", map[string]any{"error": "Your session expired. Please try again."})
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		popupResult(w, st.Origin, "error", map[string]any{"error": "Connection cancelled."})
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		popupResult(w, st.Origin, "error", map[string]any{"error": "Could not complete the connection."})
		return
	}
	clientID, clientSecret, err := s.clientCreds(r.Context())
	if err != nil {
		popupResult(w, st.Origin, "error", map[string]any{"error": "Google Drive import is not configured."})
		return
	}
	tok, err := exchangeCode(r.Context(), clientID, clientSecret, s.redirect, code)
	if err != nil {
		logging.Error(packageName, "Callback: exchange: %v", err)
		popupResult(w, st.Origin, "error", map[string]any{"error": "Could not connect to Google."})
		return
	}
	email := emailFromIDToken(tok.IDToken)
	if err := s.store.save(r.Context(), st.TenantID, st.UserID, email, tok.RefreshToken, tok.Scope); err != nil {
		logging.Error(packageName, "Callback: save: %v", err)
		popupResult(w, st.Origin, "error", map[string]any{"error": "Could not save the connection."})
		return
	}
	popupResult(w, st.Origin, "ok", map[string]any{"email": email})
}

// PickerToken hands the browser a short-lived access token plus the Picker
// developer key + app id, so it can open the Google Picker. With drive.file the
// user can browse and pick any of their files in the Picker; the app only gains
// access to the files they actually select.
func (s *Service) PickerToken(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.User == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	access, err := s.accessToken(r.Context(), id.User.ID)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "not_connected"})
		return
	}
	apiKey, _, err := s.creds.GetActive(r.Context(), providerGoogle, "api_key")
	if err != nil {
		logging.Error(packageName, "PickerToken: api key: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "picker_not_configured"})
		return
	}
	clientID, _, _ := s.clientCreds(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access,
		"api_key":      apiKey,
		"app_id":       appIDFromClientID(clientID),
		"client_id":    clientID,
	})
}

// Import downloads the selected Drive files and copies them into Trilli.
func (s *Service) Import(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.User == nil || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	var body struct {
		FileIDs     []string `json:"file_ids"`
		FolderID    *int64   `json:"folder_id"`
		WorkspaceID *int64   `json:"workspace_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
		return
	}
	if len(body.FileIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "no files selected"})
		return
	}
	if len(body.FileIDs) > maxImportFiles {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("at most %d files per import", maxImportFiles)})
		return
	}
	if !id.Access.CanWriteAt(body.FolderID, body.WorkspaceID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "you can't import into that folder"})
		return
	}
	access, err := s.accessToken(r.Context(), id.User.ID)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "not_connected"})
		return
	}

	imported := 0
	failed := make([]map[string]string, 0)
	for _, fid := range body.FileIDs {
		name, mime, err := driveMeta(r.Context(), access, fid)
		if err != nil {
			failed = append(failed, map[string]string{"id": fid, "error": "metadata"})
			continue
		}
		if mime == folderMime {
			failed = append(failed, map[string]string{"id": fid, "name": name, "error": "folders aren't supported yet"})
			continue
		}
		dlName, ct, data, err := driveDownload(r.Context(), access, fid, name, mime, maxFileBytes)
		if err != nil {
			logging.Error(packageName, "Import download %s: %v", fid, err)
			failed = append(failed, map[string]string{"id": fid, "name": name, "error": "download failed"})
			continue
		}
		if _, err := s.files.Upload(r.Context(), files.UploadInput{
			TenantID:       id.Tenant.ID,
			UploaderID:     id.User.ID,
			Name:           dlName,
			ContentType:    ct,
			Reader:         bytes.NewReader(data),
			ParentFolderID: body.FolderID,
			WorkspaceID:    body.WorkspaceID,
		}); err != nil {
			logging.Error(packageName, "Import upload %s: %v", fid, err)
			failed = append(failed, map[string]string{"id": fid, "name": name, "error": "save failed"})
			continue
		}
		imported++
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "failed": failed})
}

// Disconnect drops the user's stored Drive connection.
func (s *Service) Disconnect(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.User == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if err := s.store.delete(r.Context(), id.User.ID); err != nil {
		logging.Error(packageName, "Disconnect: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ----- helpers --------------------------------------------------------------

func allowedOrigin(o string) string {
	if strings.TrimSpace(o) == appOrigin {
		return appOrigin
	}
	return appOrigin
}

// appIDFromClientID extracts the GCP project number (the Picker appId) from a
// client id like "956881505476-abc.apps.googleusercontent.com".
func appIDFromClientID(clientID string) string {
	if i := strings.IndexByte(clientID, '-'); i > 0 {
		return clientID[:i]
	}
	return ""
}

func clearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// popupResult renders a tiny page that hands the outcome back to the opener
// (the Cloud Import modal) via BOTH localStorage (reliable: the callback page is
// same-origin with the app) and postMessage, then closes the popup.
func popupResult(w http.ResponseWriter, origin, status string, extra map[string]any) {
	if origin == "" {
		origin = appOrigin
	}
	payload := map[string]any{"source": "trilli-clouddrive", "status": status}
	for k, v := range extra {
		payload[k] = v
	}
	data, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><body><script>
(function(){var p=%s;
try{localStorage.setItem('trilli_clouddrive_result',JSON.stringify(p));}catch(e){}
try{if(window.opener)window.opener.postMessage(p,%q);}catch(e){}
window.close();})();
</script>You can close this window.</body>`, data, origin)
}
