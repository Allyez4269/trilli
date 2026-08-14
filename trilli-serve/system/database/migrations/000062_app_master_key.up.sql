-- The application master key (the root of the encryption hierarchy), stored
-- WRAPPED — never in plaintext. It is encrypted with a root key compiled into
-- the binary (system/keystore), so a database dump / stolen DB credentials /
-- the DB hyperscaler see only an opaque wrapped key they cannot open without
-- the binary. Singleton row (id is forced to 1).
CREATE TABLE IF NOT EXISTS app_master_key (
    id          INT         PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    wrapped_key BYTEA       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
