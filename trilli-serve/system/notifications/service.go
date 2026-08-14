// Package notifications stores per-user email notification preferences
// (account-wide, key/value) and a consent/audit ledger of every change —
// capturing when, from where (ip + geo), and the resulting preference snapshot
// for CAN-SPAM / GDPR / COPPA record-keeping. Sending the actual emails is a
// separate concern; this package only records intent + consent.
package notifications

import (
	"context"
	"encoding/json"
	"fmt"

	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const packageName = "notifications"

// Pref is a known notification key with its label/default. The registry is the
// source of truth: a stored row overrides the default; an absent row uses it.
type Pref struct {
	Key     string
	Default bool
}

// knownPrefs is the ordered, canonical set of notification preferences.
var knownPrefs = []Pref{
	{Key: "invite_accepted", Default: true},
	{Key: "member_removed", Default: true},
	{Key: "weekly_digest", Default: true},
	{Key: "newsletter", Default: true},
}

func isKnown(key string) bool {
	for _, p := range knownPrefs {
		if p.Key == key {
			return true
		}
	}
	return false
}

// AuditContext is the request metadata captured with each preference update.
type AuditContext struct {
	IP          string
	CountryCode string
	Country     string
	Region      string
	City        string
	Latitude    float64
	Longitude   float64
}

// Service wires the DB client.
type Service struct {
	client *postgres.Client
}

// NewService constructs the notifications Service.
func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

// Get returns the user's full preference map: code defaults overlaid with any
// stored rows.
func (s *Service) Get(ctx context.Context, userID int64) (map[string]bool, error) {
	out := make(map[string]bool, len(knownPrefs))
	for _, p := range knownPrefs {
		out[p.Key] = p.Default
	}
	rows, err := s.client.QueryContext(ctx,
		`SELECT key, enabled FROM user_notification_prefs WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("notifications: get: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var enabled bool
		if err := rows.Scan(&key, &enabled); err != nil {
			return nil, fmt.Errorf("notifications: scan: %w", err)
		}
		if isKnown(key) {
			out[key] = enabled
		}
	}
	return out, rows.Err()
}

// Update upserts the given preferences (only known keys) and appends a row to
// the consent/audit ledger capturing the request's ip + geo + the resulting
// snapshot. Returns the full saved preference map.
func (s *Service) Update(ctx context.Context, userID int64, prefs map[string]bool, audit AuditContext) (map[string]bool, error) {
	// Resulting snapshot = defaults overlaid with the submitted known keys.
	snapshot := make(map[string]bool, len(knownPrefs))
	for _, p := range knownPrefs {
		if v, ok := prefs[p.Key]; ok {
			snapshot[p.Key] = v
		} else {
			snapshot[p.Key] = p.Default
		}
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("notifications: encode snapshot: %w", err)
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("notifications: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, p := range knownPrefs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_notification_prefs (user_id, key, enabled)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, key) DO UPDATE SET enabled = EXCLUDED.enabled`,
			userID, p.Key, snapshot[p.Key],
		); err != nil {
			return nil, fmt.Errorf("notifications: upsert %s: %w", p.Key, err)
		}
	}

	var ipArg any
	if audit.IP != "" {
		ipArg = audit.IP
	}
	var latArg, lonArg any
	if audit.CountryCode != "" || audit.City != "" || audit.Latitude != 0 || audit.Longitude != 0 {
		latArg = audit.Latitude
		lonArg = audit.Longitude
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO notification_pref_changes
			(user_id, prefs, ip, country_code, country, region, city, latitude, longitude)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		userID, snapshotJSON, ipArg,
		nullStr(audit.CountryCode), nullStr(audit.Country), nullStr(audit.Region), nullStr(audit.City),
		latArg, lonArg,
	); err != nil {
		return nil, fmt.Errorf("notifications: insert audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("notifications: commit: %w", err)
	}
	committed = true

	logging.Info(packageName, "Update: user=%d prefs=%v ip=%s", userID, snapshot, audit.IP)
	return snapshot, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
