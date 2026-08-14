-- Comp / ambassador accounts (CMX, SPEC §6.10).
--
-- An operator sends a comp invite by email; clicking the link bypasses payment
-- and routes straight to a registration page that provisions a FREE tenant on
-- the granted plan (billing_mode='comp', no Stripe). The comp term expires at
-- comp_expires_at, after which the lifecycle engine drops the tenant to
-- read-only grace (reuses the lapsed behavior; data is never deleted).

-- Comp billing mode + term expiry on the tenant.
ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS billing_mode    TEXT NOT NULL DEFAULT 'paid',
    ADD COLUMN IF NOT EXISTS comp_expires_at TIMESTAMPTZ;

COMMENT ON COLUMN tenants.billing_mode IS
  'paid (Stripe-billed) | comp (complimentary, no Stripe). SPEC §6.10.';
COMMENT ON COLUMN tenants.comp_expires_at IS
  'When a comp tenant''s free term ends; lifecycle drops it to read-only grace.';

-- The invite ledger.
CREATE TABLE IF NOT EXISTS comp_invites (
    id                     BIGSERIAL PRIMARY KEY,
    token                  TEXT NOT NULL UNIQUE,         -- emailed acceptance link
    email                  TEXT NOT NULL,
    plan_code              TEXT NOT NULL,                -- granted plan (comp-eligible)
    free_term_days         INT  NOT NULL,                -- length of the comp term
    -- status: invited -> registered (account created) | expired | revoked.
    status                 TEXT NOT NULL DEFAULT 'invited'
                             CHECK (status IN ('invited', 'registered', 'expired', 'revoked')),
    invite_expires_at      TIMESTAMPTZ NOT NULL,         -- link expiry (accept by)
    invited_by_operator_id BIGINT NOT NULL,              -- cmx_operators.id (soft ref)
    invited_by_email       TEXT NOT NULL DEFAULT '',     -- attribution snapshot
    promo_note             TEXT NOT NULL DEFAULT '',     -- optional attribution / context
    tenant_id              BIGINT,                       -- set when registered
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    registered_at          TIMESTAMPTZ,
    revoked_at             TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_comp_invites_status ON comp_invites (status, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_comp_invites_email  ON comp_invites (lower(email));
