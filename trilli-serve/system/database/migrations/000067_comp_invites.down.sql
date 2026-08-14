DROP TABLE IF EXISTS comp_invites;
ALTER TABLE tenants
    DROP COLUMN IF EXISTS comp_expires_at,
    DROP COLUMN IF EXISTS billing_mode;
