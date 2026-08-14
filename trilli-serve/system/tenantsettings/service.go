// Package tenantsettings stores per-workspace policy that doesn't belong on the
// core tenants row. Today it backs the Settings → Workspace "Members & invites"
// section: the default role for new invites, whether admins may invite, and an
// optional email-domain allowlist. The invites flow reads these to enforce
// them (wired in a later step); the Settings UI reads/writes them here.
package tenantsettings

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const packageName = "tenantsettings"

// Sentinels mapped to HTTP statuses by the handlers.
var (
	ErrInvalidRole   = errors.New("tenantsettings: default role must be admin, member, or viewer")
	ErrInvalidDomain = errors.New("tenantsettings: invalid domain")
)

var validRoles = map[string]bool{"admin": true, "member": true, "viewer": true}

// domainRE accepts a bare registrable domain like "acme.com" or "acme.co.uk":
// labels of letters/digits/hyphens (not starting/ending with a hyphen), at
// least two of them. No scheme, no path, no "@".
var domainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

const maxDomains = 50

// Settings is the JSON shape exchanged with the SPA.
type Settings struct {
	DefaultInviteRole string   `json:"default_invite_role"`
	AllowAdminInvites bool     `json:"allow_admin_invites"`
	InviteDomains     []string `json:"invite_domains"`
}

// Service wires the DB client for workspace-settings reads/writes.
type Service struct {
	client *postgres.Client
}

// NewService constructs a tenantsettings Service.
func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

func defaults() *Settings {
	return &Settings{DefaultInviteRole: "member", AllowAdminInvites: true, InviteDomains: []string{}}
}

// Get returns a tenant's settings, falling back to defaults if no row exists.
func (s *Service) Get(ctx context.Context, tenantID int64) (*Settings, error) {
	out := defaults()
	var domainsCSV string
	err := s.client.QueryRowContext(ctx, `
		SELECT default_invite_role, allow_admin_invites, array_to_string(invite_domains, ',')
		  FROM tenant_settings WHERE tenant_id = $1`, tenantID,
	).Scan(&out.DefaultInviteRole, &out.AllowAdminInvites, &domainsCSV)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tenantsettings: get: %w", err)
	}
	out.InviteDomains = splitDomains(domainsCSV)
	return out, nil
}

// Update validates + upserts a tenant's settings and returns the stored result.
// callerIsOwner gates allow_admin_invites: only the owner may change who can
// invite, so for a non-owner that field is forced back to its current value.
func (s *Service) Update(ctx context.Context, tenantID int64, callerIsOwner bool, in Settings) (*Settings, error) {
	role := strings.ToLower(strings.TrimSpace(in.DefaultInviteRole))
	if !validRoles[role] {
		return nil, ErrInvalidRole
	}
	domains, err := normalizeDomains(in.InviteDomains)
	if err != nil {
		return nil, err
	}

	allow := in.AllowAdminInvites
	if !callerIsOwner {
		cur, err := s.Get(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		allow = cur.AllowAdminInvites // non-owner can't change this field
	}

	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO tenant_settings (tenant_id, default_invite_role, allow_admin_invites, invite_domains, updated_at)
		VALUES ($1, $2, $3, $4::text[], NOW())
		ON CONFLICT (tenant_id) DO UPDATE
		   SET default_invite_role = EXCLUDED.default_invite_role,
		       allow_admin_invites = EXCLUDED.allow_admin_invites,
		       invite_domains      = EXCLUDED.invite_domains,
		       updated_at          = NOW()`,
		tenantID, role, allow, stringArray(domains),
	); err != nil {
		return nil, fmt.Errorf("tenantsettings: update: %w", err)
	}
	logging.Info(packageName, "Update: tenant=%d role=%s allowAdmin=%v domains=%v", tenantID, role, allow, domains)
	return &Settings{DefaultInviteRole: role, AllowAdminInvites: allow, InviteDomains: domains}, nil
}

// splitDomains turns the array_to_string CSV back into a clean slice.
func splitDomains(csv string) []string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return []string{}
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// normalizeDomains lowercases, strips a leading "@", validates, and dedupes the
// allowlist. Returns ErrInvalidDomain (wrapping the offending entry) on any bad
// value so the user gets a specific message.
func normalizeDomains(in []string) ([]string, error) {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		d = strings.TrimPrefix(d, "@")
		if d == "" {
			continue
		}
		if !domainRE.MatchString(d) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidDomain, d)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
		if len(out) >= maxDomains {
			break
		}
	}
	return out, nil
}

// stringArray renders []string as a Postgres array literal for $N::text[].
// (Mirrors the int64Slice helper used elsewhere — the database/sql layer over
// pgx stdlib wants an explicit driver.Valuer.)
type stringArray []string

func (a stringArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, v := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		b.WriteByte('"')
		b.WriteString(v)
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String(), nil
}
