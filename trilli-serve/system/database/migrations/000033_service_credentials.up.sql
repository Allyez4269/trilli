-- Encrypted vault for third-party service credentials (Stripe, future APIs).
-- value_enc holds AES-GCM ciphertext (system/crypto, keyed by APP_ENCRYPTION_KEY);
-- last4 is the plaintext tail for masked display without decrypting. The DB
-- connection itself stays baked in code and APP_ENCRYPTION_KEY lives in the
-- systemd drop-in — this table holds everything else. Managed by super-admins.
CREATE TABLE IF NOT EXISTS service_credentials (
    id          BIGSERIAL   PRIMARY KEY,
    provider    TEXT        NOT NULL,                 -- 'stripe'
    key_name    TEXT        NOT NULL,                 -- 'secret_key' | 'publishable_key' | 'webhook_secret'
    environment TEXT        NOT NULL DEFAULT 'test',  -- 'test' | 'live'
    value_enc   BYTEA       NOT NULL,                 -- AES-GCM ciphertext
    last4       TEXT        NOT NULL DEFAULT '',      -- plaintext tail for masked UI
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT service_credentials_env_chk CHECK (environment IN ('test','live')),
    UNIQUE (provider, key_name, environment)
);
