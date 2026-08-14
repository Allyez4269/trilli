-- Productivity (office suite) settings.
--
-- Two layers, mirroring the existing patterns:
--   * tenant_productivity_settings  — per-tenant, admin-owned config (like
--     tenant_settings: one typed row per tenant, + a JSONB escape hatch for
--     config that evolves during the POC without a migration each time).
--   * user_productivity_prefs       — per-user personal prefs (like
--     user_notification_prefs: key/value, absent row falls back to the tenant
--     default, then the code default). Lives in the DB so a user's prefs
--     (e.g. zoom) follow them across devices — localStorage is only a cache.

CREATE TABLE tenant_productivity_settings (
    tenant_id    BIGINT PRIMARY KEY REFERENCES tenants(id) ON UPDATE CASCADE ON DELETE CASCADE,
    enabled      BOOLEAN NOT NULL DEFAULT false,         -- is Productivity on for this tenant
    allowlist    TEXT[]  NOT NULL DEFAULT '{}',          -- emails allowed (empty = all active members when enabled)
    default_zoom INTEGER NOT NULL DEFAULT 9,             -- engine zoom level (9 = 85%); org default
    settings     JSONB   NOT NULL DEFAULT '{}'::jsonb,   -- evolving config (enabled apps, branding, save target, flags)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT tenant_prod_zoom_chk CHECK (default_zoom BETWEEN 1 AND 18)
);

CREATE TABLE user_productivity_prefs (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key        TEXT   NOT NULL,
    value      TEXT   NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, key)
);

-- Seed: turn Productivity ON for the tenant(s) where mike@prices.com is a member,
-- allowlisted to him — preserves the current "only mike@prices.com" gate while
-- moving it out of hardcoded JS and into the DB.
INSERT INTO tenant_productivity_settings (tenant_id, enabled, allowlist)
SELECT DISTINCT tm.tenant_id, true, ARRAY['mike@prices.com']::text[]
FROM tenant_members tm
JOIN users u ON u.id = tm.user_id
WHERE u.email = 'mike@prices.com'
ON CONFLICT (tenant_id) DO NOTHING;
