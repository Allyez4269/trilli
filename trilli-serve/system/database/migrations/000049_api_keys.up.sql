-- API keys for programmatic account access (plan-gated to tiers with api_access).
-- Only a SHA-256 hash of the secret is stored; the plaintext is shown once at
-- creation. key_prefix + last_four are kept for display/identification.
CREATE TABLE IF NOT EXISTS api_keys (
    id                 BIGSERIAL PRIMARY KEY,
    tenant_id          BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    created_by_user_id BIGINT NOT NULL REFERENCES users(id),
    name               TEXT NOT NULL,
    key_prefix         TEXT NOT NULL,
    key_hash           TEXT NOT NULL,
    last_four          TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at       TIMESTAMPTZ,
    revoked_at         TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS api_keys_key_hash_idx ON api_keys (key_hash);
CREATE INDEX IF NOT EXISTS api_keys_tenant_active_idx ON api_keys (tenant_id) WHERE revoked_at IS NULL;
