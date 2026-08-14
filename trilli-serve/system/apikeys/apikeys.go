// Package apikeys issues and authenticates per-account API keys for Trilli's
// programmatic REST surface (/api/v1). Keys are plan-gated: only tenants whose
// plan has api_access may create or use them.
package apikeys

import (
	"errors"
	"time"
)

const packageName = "apikeys"

// KeyPrefix is the human-recognizable, non-secret prefix every token carries.
const KeyPrefix = "trilli_sk_"

// APIKey is the stored, non-secret metadata for a key (never the secret itself).
type APIKey struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	KeyPrefix       string     `json:"key_prefix"`
	LastFour        string     `json:"last_four"`
	CreatedByUserID int64      `json:"created_by_user_id"`
	CreatedByName   string     `json:"created_by_name,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
}

// Principal is the resolved context for an authenticated API-key request.
type Principal struct {
	TenantID  int64
	UserID    int64
	KeyID     int64
	APIAccess bool
	PlanName  string
}

// Sentinel errors. Handlers map them to HTTP statuses.
var (
	ErrNotFound    = errors.New("apikeys: not found")
	ErrInvalidKey  = errors.New("apikeys: invalid or revoked key")
	ErrInvalidName = errors.New("apikeys: invalid name")
)
