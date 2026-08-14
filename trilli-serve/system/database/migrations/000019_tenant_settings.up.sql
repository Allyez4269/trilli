-- tenant_settings holds per-workspace policy that doesn't belong on the core
-- tenants row. First use: the "Members & invites" defaults — the default role
-- applied to new invites, whether admins (not just the owner) may invite, and
-- an optional email-domain allowlist for invitees.
--
-- One row per tenant. Backfilled for every existing tenant below; signup
-- inserts a row for new tenants; reads fall back to defaults if a row is
-- somehow missing.

CREATE TABLE tenant_settings (
    tenant_id           BIGINT PRIMARY KEY REFERENCES tenants(id) ON UPDATE CASCADE ON DELETE CASCADE,
    default_invite_role TEXT    NOT NULL DEFAULT 'member',
    allow_admin_invites BOOLEAN NOT NULL DEFAULT true,
    invite_domains      TEXT[]  NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT tenant_settings_role_chk
        CHECK (default_invite_role = ANY (ARRAY['admin'::text, 'member'::text, 'viewer'::text]))
);

-- Backfill a default row for every existing workspace.
INSERT INTO tenant_settings (tenant_id)
SELECT id FROM tenants
ON CONFLICT (tenant_id) DO NOTHING;
