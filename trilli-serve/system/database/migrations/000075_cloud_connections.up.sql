-- Per-user connections to external cloud storage for the Cloud Import feature
-- (Google Drive first; OneDrive/Dropbox later). Holds the OAuth refresh token
-- encrypted at rest (system/crypto, keyed by APP_ENCRYPTION_KEY) so the app can
-- mint short-lived access tokens to browse and copy the user's files into Trilli.
--
-- The provider's client id/secret live in service_credentials
-- (provider='google_drive'); THIS table holds the per-user grants. One active
-- row per (user, provider) — reconnecting upserts.
CREATE TABLE IF NOT EXISTS cloud_connections (
    id                BIGSERIAL   PRIMARY KEY,
    tenant_id         BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id           BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider          TEXT        NOT NULL,             -- 'google_drive'
    account_email     TEXT        NOT NULL DEFAULT '',  -- connected account, for display
    refresh_token_enc BYTEA       NOT NULL,             -- AES-GCM ciphertext
    scope             TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, provider)
);

CREATE INDEX IF NOT EXISTS cloud_connections_tenant_idx ON cloud_connections (tenant_id);
