// Package appadmin is CMX's client for the app's /api/admin/* surface (the
// Option-C write path, SPEC §7/§8b). CMX authenticates as a service principal:
// it reads the shared service token from the encrypted credentials vault (the
// same service_credentials row the app validates against) and sends it in the
// X-CMX-Service-Token header, along with the acting operator's identity for
// app-side attribution.
package appadmin

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"trilli-cmx/system/crypto"
	"trilli-cmx/system/database/postgres"
	"trilli-cmx/system/logging"
)

const packageName = "appadmin"

// Vault coordinates for the CMX service token (must match system/adminapi).
const (
	credProvider = "cmx"
	credKeyName  = "service_token"
	credEnv      = "live"
)

// ErrNotConfigured means no service token is provisioned in the vault.
var ErrNotConfigured = errors.New("appadmin: CMX service token not provisioned")

// ActingOperator identifies the operator on whose behalf a write is made; sent
// to the app for attribution (CMX keeps the authoritative audit).
type ActingOperator struct {
	ID    int64
	Email string
}

// Client calls the app's admin API.
type Client struct {
	db      *postgres.Client
	baseURL string
	http    *http.Client

	mu    sync.RWMutex
	token string // cached vault token
}

// NewClient builds the app-admin client. The base URL comes from CMX_APP_ADMIN_URL
// (default https://127.0.0.1:8081 — the production app on this host). The app now
// terminates TLS in-process, so we reach it over HTTPS even on the loopback; we
// dial 127.0.0.1 but verify the wildcard cert under its real name (app.trilli.com)
// against the system roots — no skipped verification.
func NewClient(db *postgres.Client) *Client {
	base := strings.TrimSpace(os.Getenv("CMX_APP_ADMIN_URL"))
	if base == "" {
		base = "https://127.0.0.1:8081"
	}
	return &Client{
		db:      db,
		baseURL: strings.TrimRight(base, "/"),
		http: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{ServerName: "app.trilli.com"},
			},
		},
	}
}

// token resolves (and caches) the service token from the vault.
func (c *Client) serviceToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	tok := c.token
	c.mu.RUnlock()
	if tok != "" {
		return tok, nil
	}
	var enc []byte
	err := c.db.QueryRowContext(ctx,
		`SELECT value_enc FROM service_credentials
		  WHERE provider = $1 AND key_name = $2 AND environment = $3`,
		credProvider, credKeyName, credEnv,
	).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotConfigured
	}
	if err != nil {
		return "", fmt.Errorf("appadmin: read token: %w", err)
	}
	plain, derr := crypto.Decrypt(enc)
	if derr != nil {
		return "", fmt.Errorf("appadmin: decrypt token: %w", derr)
	}
	c.mu.Lock()
	c.token = string(plain)
	c.mu.Unlock()
	return string(plain), nil
}

// post issues an authenticated POST with no response decoding.
func (c *Client) post(ctx context.Context, path string, actor ActingOperator, action string, body any) error {
	return c.do(ctx, http.MethodPost, path, actor, action, body, nil)
}

// postMethod issues an authenticated request with an explicit method (e.g. PATCH).
func (c *Client) postMethod(ctx context.Context, method, path string, actor ActingOperator, action string, body any) error {
	return c.do(ctx, method, path, actor, action, body, nil)
}

// postJSON issues an authenticated POST and decodes the JSON response into out.
func (c *Client) postJSON(ctx context.Context, path string, actor ActingOperator, action string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, actor, action, body, out)
}

// do performs an authenticated request to /api/admin/<path> with the acting
// operator headers and an optional JSON body, decoding a {error} on non-2xx
// (and into out on success when out != nil).
func (c *Client) do(ctx context.Context, method, path string, actor ActingOperator, action string, body, out any) error {
	tok, err := c.serviceToken(ctx)
	if err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			return fmt.Errorf("appadmin: marshal: %w", merr)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("appadmin: build request: %w", err)
	}
	req.Header.Set("X-CMX-Service-Token", tok)
	req.Header.Set("X-CMX-Operator-Email", actor.Email)
	req.Header.Set("X-CMX-Operator-Id", strconv.FormatInt(actor.ID, 10))
	req.Header.Set("X-CMX-Action", action)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("appadmin: call %s: %w", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	// Guard against a non-upgraded app: an unknown /api/admin/* path falls
	// through to the app's SPA catch-all and returns 200 + HTML. Require a JSON
	// content type so that never reads as a (silent) success.
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		logging.Error(packageName, "%s -> non-JSON response (%s); app may not expose /api/admin", path, ct)
		return fmt.Errorf("the app admin surface is unavailable (is app.trilli.com upgraded?)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error
		if msg == "" {
			msg = fmt.Sprintf("app returned %d", resp.StatusCode)
		}
		logging.Error(packageName, "%s -> %d: %s", path, resp.StatusCode, msg)
		return fmt.Errorf("%s", msg)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("appadmin: decode response: %w", err)
		}
	}
	return nil
}

// SuspendTenant suspends a tenant via the app.
func (c *Client) SuspendTenant(ctx context.Context, tenantID int64, actor ActingOperator) error {
	return c.post(ctx, fmt.Sprintf("/api/admin/tenants/%d/suspend", tenantID), actor, "tenant.suspend", nil)
}

// ReactivateTenant reactivates a suspended tenant via the app.
func (c *Client) ReactivateTenant(ctx context.Context, tenantID int64, actor ActingOperator) error {
	return c.post(ctx, fmt.Sprintf("/api/admin/tenants/%d/reactivate", tenantID), actor, "tenant.reactivate", nil)
}

// SetQuotaOverride sets (bytes != nil) or clears (bytes == nil) a tenant's
// storage cap override via the app.
func (c *Client) SetQuotaOverride(ctx context.Context, tenantID int64, bytes *int64, actor ActingOperator) error {
	return c.post(ctx, fmt.Sprintf("/api/admin/tenants/%d/quota-override", tenantID), actor,
		"tenant.quota_override", map[string]any{"bytes": bytes})
}

// ---- Catalog plan writes (SPEC §6.3) ----

// CreatePlan creates a draft plan via the app and returns the new id.
func (c *Client) CreatePlan(ctx context.Context, in map[string]any, actor ActingOperator) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.postJSON(ctx, "/api/admin/plans", actor, "plan.create", in, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// UpdatePlan edits a plan's fields via the app.
func (c *Client) UpdatePlan(ctx context.Context, planID int64, in map[string]any, actor ActingOperator) error {
	return c.postMethod(ctx, http.MethodPatch, fmt.Sprintf("/api/admin/plans/%d", planID), actor, "plan.update", in)
}

// SetPlanStatus transitions a plan's lifecycle status via the app.
func (c *Client) SetPlanStatus(ctx context.Context, planID int64, status string, actor ActingOperator) error {
	return c.post(ctx, fmt.Sprintf("/api/admin/plans/%d/status", planID), actor, "plan.status",
		map[string]any{"status": status})
}

// ---- Comp / ambassador invites (SPEC §6.10) ----

// CreateCompInvite sends a comp invite via the app and returns the new id.
func (c *Client) CreateCompInvite(ctx context.Context, in map[string]any, actor ActingOperator) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.postJSON(ctx, "/api/admin/comp-invites", actor, "comp.invite", in, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// RevokeCompInvite revokes an unredeemed comp invite via the app.
func (c *Client) RevokeCompInvite(ctx context.Context, inviteID int64, actor ActingOperator) error {
	return c.post(ctx, fmt.Sprintf("/api/admin/comp-invites/%d/revoke", inviteID), actor, "comp.revoke", nil)
}

// DeleteCompInvite permanently removes a comp invite record via the app.
func (c *Client) DeleteCompInvite(ctx context.Context, inviteID int64, actor ActingOperator) error {
	return c.post(ctx, fmt.Sprintf("/api/admin/comp-invites/%d/delete", inviteID), actor, "comp.delete", nil)
}

// ---- Revenue money-writes (SPEC §6.4) ----

// RefundResult mirrors the app's refund response.
type RefundResult struct {
	RefundedCents int64  `json:"refunded_cents"`
	RefundID      string `json:"refund_id"`
}

// Refund issues a refund against a tenant's transaction via the app. amountCents
// <= 0 refunds the full remaining amount.
func (c *Client) Refund(ctx context.Context, tenantID, transactionID, amountCents int64, actor ActingOperator) (RefundResult, error) {
	var out struct {
		RefundedCents int64  `json:"refunded_cents"`
		RefundID      string `json:"refund_id"`
	}
	err := c.postJSON(ctx, fmt.Sprintf("/api/admin/tenants/%d/refund", tenantID), actor, "revenue.refund",
		map[string]any{"transaction_id": transactionID, "amount_cents": amountCents}, &out)
	if err != nil {
		return RefundResult{}, err
	}
	return RefundResult{RefundedCents: out.RefundedCents, RefundID: out.RefundID}, nil
}

// GrantCredit grants account credit to a tenant via the app.
func (c *Client) GrantCredit(ctx context.Context, tenantID, amountCents int64, reason string, actor ActingOperator) error {
	return c.post(ctx, fmt.Sprintf("/api/admin/tenants/%d/credits", tenantID), actor, "revenue.credit",
		map[string]any{"amount_cents": amountCents, "reason": reason})
}

// ReconcileSubscription force-pulls the tenant's subscription from Stripe via the app.
func (c *Client) ReconcileSubscription(ctx context.Context, tenantID int64, actor ActingOperator) error {
	return c.post(ctx, fmt.Sprintf("/api/admin/tenants/%d/reconcile-subscription", tenantID), actor, "revenue.reconcile", nil)
}

// SetAutoRenew cancels (enabled=false) or resumes (enabled=true) a tenant's
// subscription at term-end via the app.
func (c *Client) SetAutoRenew(ctx context.Context, tenantID int64, enabled bool, actor ActingOperator) error {
	return c.post(ctx, fmt.Sprintf("/api/admin/tenants/%d/auto-renew", tenantID), actor, "revenue.auto_renew",
		map[string]any{"enabled": enabled})
}

// ChangePlanResult mirrors the app's change-plan response.
type ChangePlanResult struct {
	Effect      string  `json:"effect"`       // upgraded | scheduled
	EffectiveAt *string `json:"effective_at"` // RFC3339, may be null
	PlanName    string  `json:"plan_name"`
}

// ChangePlan moves a tenant's subscription to a new plan/period via the app.
func (c *Client) ChangePlan(ctx context.Context, tenantID int64, planCode, period string, actor ActingOperator) (ChangePlanResult, error) {
	var out ChangePlanResult
	err := c.postJSON(ctx, fmt.Sprintf("/api/admin/tenants/%d/change-plan", tenantID), actor, "revenue.change_plan",
		map[string]any{"plan_code": planCode, "billing_period": period}, &out)
	return out, err
}

// ---- Support desk (SPEC §6.8) ----

// TicketReply posts an operator reply (internal=false, emailed to the customer
// as "Trilli Support", with an optional status) or an internal note
// (internal=true, operator-only) to a ticket via the app.
func (c *Client) TicketReply(ctx context.Context, ticketID int64, body, status string, internal bool, actor ActingOperator) error {
	action := "support.reply"
	if internal {
		action = "support.note"
	}
	return c.post(ctx, fmt.Sprintf("/api/admin/tickets/%d/reply", ticketID), actor, action,
		map[string]any{"body": body, "status": status, "internal": internal})
}

// ---- Infrastructure write ops (SPEC §6.5) ----

// JobRunResult mirrors the app's run-now response.
type JobRunResult struct {
	Ran  bool   `json:"ran"`
	Node string `json:"node"`
	Job  string `json:"job"`
}

// RunJob triggers a named background job immediately via the app (cluster-locked).
func (c *Client) RunJob(ctx context.Context, job string, actor ActingOperator) (JobRunResult, error) {
	var out JobRunResult
	err := c.postJSON(ctx, fmt.Sprintf("/api/admin/jobs/%s/run", job), actor, "infra.job_run", nil, &out)
	return out, err
}

// Storage tiering runs automatically via the app's scheduled engine
// (TRILLI_TIERING_MODE) — there is no operator-triggered tiering pass; CMX shows
// its status (distribution + last run) read-only.
