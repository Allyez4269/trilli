-- Sharing Sh3: drop portals (a.k.a. file requests) — the inbound counterpart to
-- shares. A public token-gated endpoint where a 3rd party uploads files INTO a
-- destination folder without a Trilli account. Distinct primitive from a share
-- (inbound/upload vs outbound/download), so its own table.
--
-- Uploads land in folder_id, attributed to created_by_user_id (the owner
-- "receives" them) and counted against the owner's storage quota + transfer
-- ingress via the normal files.Upload path. Portals go dark when the owning
-- account is suspended or lapsed.
CREATE TABLE IF NOT EXISTS drop_portals (
    id                 BIGSERIAL   PRIMARY KEY,
    tenant_id          BIGINT      NOT NULL REFERENCES tenants(id)  ON DELETE CASCADE,
    token              TEXT        NOT NULL UNIQUE,
    folder_id          BIGINT      NOT NULL REFERENCES folders(id) ON DELETE CASCADE,  -- destination
    title              TEXT        NOT NULL DEFAULT '',
    password_hash      TEXT,
    expires_at         TIMESTAMPTZ,
    created_by_user_id BIGINT      NOT NULL REFERENCES users(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at         TIMESTAMPTZ,
    upload_count       BIGINT      NOT NULL DEFAULT 0,
    last_upload_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_drop_portals_tenant ON drop_portals (tenant_id) WHERE revoked_at IS NULL;
