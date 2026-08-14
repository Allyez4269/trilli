-- Per-tenant data-encryption keys (DEKs) for blob encryption. Each tenant's
-- file bytes are encrypted with its own 256-bit DEK; the DEK is stored here
-- WRAPPED (encrypted) by the app's key-encryption-key (derived from
-- APP_ENCRYPTION_KEY), never in plaintext. Azure holds the ciphertext; this
-- table (on a different provider's Postgres) holds the wrapped keys; the KEK
-- lives only in the app environment — so no single party but Trilli can read
-- the data. Destroying a tenant's row crypto-shreds their files.
CREATE TABLE IF NOT EXISTS tenant_file_keys (
    tenant_id   BIGINT      PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    wrapped_dek BYTEA       NOT NULL,   -- DEK sealed with the KEK (crypto.Encrypt)
    key_version INT         NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Tracks which blobs have been re-encrypted by the background migration. New
-- uploads are written encrypted; legacy blobs are detected on read by their
-- header and migrated over time.
ALTER TABLE files
    ADD COLUMN IF NOT EXISTS encrypted BOOLEAN NOT NULL DEFAULT FALSE;
