package clouddrive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	driveAPI       = "https://www.googleapis.com/drive/v3"
	// drive.file is NON-sensitive: any user can authorize with no Google
	// verification or test-user allowlist. The app only gets access to files the
	// user explicitly selects in the Google Picker — which is exactly the import
	// flow. (drive.readonly, which a custom Drive browser needs, is "restricted"
	// and would require Google's security assessment for production.)
	driveScope = "https://www.googleapis.com/auth/drive.file"
	folderMime = "application/vnd.google-apps.folder"
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// exchangeCode swaps an authorization code for tokens, server-to-server over TLS.
func exchangeCode(ctx context.Context, clientID, clientSecret, redirect, code string) (*tokenResponse, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("redirect_uri", redirect)
	form.Set("grant_type", "authorization_code")
	return postToken(ctx, form)
}

// refreshAccess mints a fresh, short-lived access token from a stored refresh token.
func refreshAccess(ctx context.Context, clientID, clientSecret, refreshToken string) (string, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")
	tok, err := postToken(ctx, form)
	if err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

func postToken(ctx context.Context, form url.Values) (*tokenResponse, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google token %d: %s", resp.StatusCode, string(body))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decode token: %w", err)
	}
	return &tr, nil
}

// emailFromIDToken reads the email claim from a Google id_token. Signature
// verification is unnecessary: the token came over TLS from Google's token
// endpoint using our client secret.
func emailFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var c struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(payload, &c)
	return c.Email
}

// ----- Drive API -----------------------------------------------------------
//
// With the drive.file scope the app can only touch files the user picked in the
// Google Picker; there's no folder browsing here — the Picker handles selection.
// We only need per-file metadata + download for the import.

// driveMeta fetches a single file's name and mime type (so the importer never
// trusts client-supplied names).
func driveMeta(ctx context.Context, accessToken, fileID string) (name, mime string, err error) {
	q := url.Values{}
	q.Set("fields", "id,name,mimeType")
	q.Set("supportsAllDrives", "true")
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, driveAPI+"/files/"+url.PathEscape(fileID)+"?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("drive meta %d: %s", resp.StatusCode, string(body))
	}
	var m struct {
		Name     string `json:"name"`
		MimeType string `json:"mimeType"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return "", "", err
	}
	return m.Name, m.MimeType, nil
}

// googleExports maps native Google Docs editor types to a downloadable
// Office/PDF format (Drive can't return their raw bytes via alt=media).
var googleExports = map[string]struct{ mime, ext string }{
	"application/vnd.google-apps.document":     {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
	"application/vnd.google-apps.spreadsheet":  {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx"},
	"application/vnd.google-apps.presentation": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx"},
	"application/vnd.google-apps.drawing":      {"application/pdf", ".pdf"},
}

// driveDownload fetches a file's bytes. Native Google Docs are exported to an
// Office/PDF format (extension appended); everything else is downloaded as-is.
// Returns the (possibly adjusted) name, content type, and bytes.
func driveDownload(ctx context.Context, accessToken, fileID, name, mime string, maxBytes int64) (string, string, []byte, error) {
	var endpoint, contentType string
	switch {
	case googleExports[mime].mime != "":
		exp := googleExports[mime]
		q := url.Values{}
		q.Set("mimeType", exp.mime)
		endpoint = driveAPI + "/files/" + url.PathEscape(fileID) + "/export?" + q.Encode()
		contentType = exp.mime
		if !strings.HasSuffix(strings.ToLower(name), exp.ext) {
			name += exp.ext
		}
	case strings.HasPrefix(mime, "application/vnd.google-apps."):
		// Other native types (forms, sites, …) — best-effort PDF export.
		q := url.Values{}
		q.Set("mimeType", "application/pdf")
		endpoint = driveAPI + "/files/" + url.PathEscape(fileID) + "/export?" + q.Encode()
		contentType = "application/pdf"
		if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
			name += ".pdf"
		}
	default:
		q := url.Values{}
		q.Set("alt", "media")
		q.Set("supportsAllDrives", "true")
		endpoint = driveAPI + "/files/" + url.PathEscape(fileID) + "?" + q.Encode()
		contentType = mime
	}

	cctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", "", nil, fmt.Errorf("drive download %d: %s", resp.StatusCode, string(b))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", "", nil, err
	}
	if int64(len(data)) > maxBytes {
		return "", "", nil, fmt.Errorf("file exceeds the %d-byte import limit", maxBytes)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return name, contentType, data, nil
}
