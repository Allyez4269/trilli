-- CMX CRM relationship layer + platform flags (SPEC §4, §4.1-B).
--
-- A Customer is the durable RELATIONSHIP record (who Trilli sells to); the set
-- of Accounts/tenants a customer owns is DERIVED from tenant ownership, never
-- duplicated here, so it can't drift (SPEC §4.1 note).

-- ---------------------------------------------------------------------------
-- cmx_customers — the CRM relationship record (SPEC §6.1).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_customers (
    id               BIGSERIAL PRIMARY KEY,
    -- The owning identity in the app's users table. Nullable for pure leads
    -- that have no account yet. Soft ref (no FK) so a user delete never
    -- cascade-erases the CRM relationship history.
    identity_user_id BIGINT,
    display_name     TEXT NOT NULL DEFAULT '',
    company          TEXT NOT NULL DEFAULT '',
    -- lifecycle_stage: lead -> trial -> paying -> churned (SPEC §4).
    lifecycle_stage  TEXT NOT NULL DEFAULT 'lead'
                       CHECK (lifecycle_stage IN ('lead', 'trial', 'paying', 'churned')),
    primary_email    TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_cmx_customers_identity
    ON cmx_customers (identity_user_id) WHERE identity_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS ix_cmx_customers_email
    ON cmx_customers (lower(primary_email));
CREATE INDEX IF NOT EXISTS ix_cmx_customers_stage
    ON cmx_customers (lifecycle_stage);

-- ---------------------------------------------------------------------------
-- cmx_customer_notes — freeform operator notes on a customer (SPEC §6.1).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_customer_notes (
    id          BIGSERIAL PRIMARY KEY,
    customer_id BIGINT NOT NULL REFERENCES cmx_customers(id) ON DELETE CASCADE,
    operator_id BIGINT NOT NULL,          -- soft ref to cmx_operators.id
    body        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_cmx_customer_notes_customer
    ON cmx_customer_notes (customer_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- cmx_platform_flags — CMX-side key/value settings (SPEC §4.1-B). E.g. the
-- Maintenance Mode intent before it is pushed to the app. Audited via
-- cmx_operator_audit on write.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_platform_flags (
    key         TEXT PRIMARY KEY,
    value       JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_by  BIGINT,                   -- soft ref to cmx_operators.id
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
