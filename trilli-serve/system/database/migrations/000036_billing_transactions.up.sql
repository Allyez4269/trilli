-- Our own order record per successful purchase. Stripe owns the money
-- identifiers (payment_intent / charge / receipt); we own the order: a stable
-- internal id, a human-friendly order number, and the Stripe references +
-- receipt links for support, reconciliation, and the thank-you page.
CREATE TABLE IF NOT EXISTS billing_transactions (
    id                       BIGSERIAL PRIMARY KEY,
    tenant_id                BIGINT NOT NULL REFERENCES tenants(id),
    plan_code                TEXT   NOT NULL,
    billing_period           TEXT   NOT NULL,
    amount_cents             BIGINT NOT NULL,
    currency                 TEXT   NOT NULL DEFAULT 'usd',
    stripe_payment_intent_id TEXT   NOT NULL UNIQUE,
    stripe_charge_id         TEXT   NOT NULL DEFAULT '',
    receipt_url              TEXT   NOT NULL DEFAULT '',
    receipt_number           TEXT   NOT NULL DEFAULT '',
    order_number             TEXT   UNIQUE,
    status                   TEXT   NOT NULL DEFAULT 'succeeded',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_billing_tx_tenant ON billing_transactions (tenant_id, created_at DESC);
