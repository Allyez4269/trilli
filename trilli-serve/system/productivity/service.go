// Package productivity stores settings for the Trilli Productivity (office)
// suite, in two layers:
//   - tenant_productivity_settings : per-tenant, admin-owned config (enabled,
//     allowlist, default zoom, + a JSONB escape hatch).
//   - user_productivity_prefs      : per-user key/value prefs (zoom, units, …)
//     that follow the user across devices (localStorage is only a cache).
//
// Resolution order for any value: user pref → tenant default → code default.
package productivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"trilli/system/database/postgres"
)

// TenantSettings is the per-tenant config (also the JSON shape for the admin API).
type TenantSettings struct {
	Enabled     bool            `json:"enabled"`
	Allowlist   []string        `json:"allowlist"`
	DefaultZoom int             `json:"default_zoom"`
	Settings    json.RawMessage `json:"settings"`
}

type Service struct {
	client *postgres.Client
}

func NewService(c *postgres.Client) *Service { return &Service{client: c} }

func defaultTenant() *TenantSettings {
	return &TenantSettings{Enabled: false, Allowlist: []string{}, DefaultZoom: 9, Settings: json.RawMessage(`{}`)}
}

// GetTenant returns a tenant's settings, falling back to defaults if no row.
func (s *Service) GetTenant(ctx context.Context, tenantID int64) (*TenantSettings, error) {
	out := defaultTenant()
	var allowCSV string
	var settings []byte
	err := s.client.QueryRowContext(ctx, `
		SELECT enabled, array_to_string(allowlist, ','), default_zoom, settings
		  FROM tenant_productivity_settings WHERE tenant_id = $1`, tenantID,
	).Scan(&out.Enabled, &allowCSV, &out.DefaultZoom, &settings)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if allowCSV != "" {
		out.Allowlist = strings.Split(allowCSV, ",")
	}
	if len(settings) > 0 {
		out.Settings = json.RawMessage(settings)
	}
	return out, nil
}

// UpsertTenant writes a tenant's settings (admin action).
func (s *Service) UpsertTenant(ctx context.Context, tenantID int64, in *TenantSettings) error {
	zoom := in.DefaultZoom
	if zoom < 1 || zoom > 18 {
		zoom = 9
	}
	allow := strings.Join(in.Allowlist, ",")
	settings := in.Settings
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}
	_, err := s.client.ExecContext(ctx, `
		INSERT INTO tenant_productivity_settings (tenant_id, enabled, allowlist, default_zoom, settings, updated_at)
		VALUES ($1, $2, CASE WHEN $3 = '' THEN '{}'::text[] ELSE string_to_array($3, ',') END, $4, $5::jsonb, NOW())
		ON CONFLICT (tenant_id) DO UPDATE
		   SET enabled      = EXCLUDED.enabled,
		       allowlist    = EXCLUDED.allowlist,
		       default_zoom = EXCLUDED.default_zoom,
		       settings     = EXCLUDED.settings,
		       updated_at   = NOW()`,
		tenantID, in.Enabled, allow, zoom, string(settings),
	)
	return err
}

// GetUserPrefs returns a user's productivity prefs as a key/value map.
func (s *Service) GetUserPrefs(ctx context.Context, userID int64) (map[string]string, error) {
	rows, err := s.client.QueryContext(ctx, `SELECT key, value FROM user_productivity_prefs WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetUserPref upserts a single user pref.
func (s *Service) SetUserPref(ctx context.Context, userID int64, key, value string) error {
	_, err := s.client.ExecContext(ctx, `
		INSERT INTO user_productivity_prefs (user_id, key, value, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		userID, key, value,
	)
	return err
}

// IsAllowed reports whether the given email may use Productivity in this tenant.
func (t *TenantSettings) IsAllowed(email string) bool {
	if !t.Enabled {
		return false
	}
	if len(t.Allowlist) == 0 {
		return true // enabled for all active members
	}
	email = strings.ToLower(strings.TrimSpace(email))
	for _, e := range t.Allowlist {
		if strings.ToLower(strings.TrimSpace(e)) == email {
			return true
		}
	}
	return false
}
