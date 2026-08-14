-- Account-credit ledger (SPEC §6.4 — "no credit store exists; needs a
-- first-class credit ledger"). Each row records an operator GRANT of account
-- credit. The credit is actually applied to future invoices via the tenant's
-- Stripe customer balance (a negative balance Stripe auto-consumes at billing
-- time); this table is Trilli's authoritative audit + display ledger of grants.
--
-- Amounts are POSITIVE cents = credit granted to the customer. currency mirrors
-- the tenant's billing currency. stripe_balance_txn_id links the grant to the
-- Stripe customer-balance transaction that effected it (empty if Stripe was
-- unavailable / billing disabled, in which case the grant is ledger-only).
CREATE TABLE IF NOT EXISTS account_credits (
    id                     BIGSERIAL PRIMARY KEY,
    tenant_id              BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    amount_cents           BIGINT NOT NULL CHECK (amount_cents > 0),
    currency               TEXT   NOT NULL DEFAULT 'usd',
    reason                 TEXT   NOT NULL DEFAULT '',
    stripe_balance_txn_id  TEXT   NOT NULL DEFAULT '',
    granted_by_operator_id TEXT   NOT NULL DEFAULT '',
    granted_by_email       TEXT   NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_account_credits_tenant
    ON account_credits (tenant_id, created_at DESC);
