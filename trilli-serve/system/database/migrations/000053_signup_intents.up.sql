-- signup_intents backs the pay-before-account registration funnel. NO row in
-- users/tenants is created until payment is secured; until then the entire
-- pre-account funnel state lives here so an abandoner leaves no orphan and
-- their email frees up automatically (cleanup purges expired/abandoned rows).
--
-- Lifecycle: pending_email -> email_verified -> paid -> completed.
--   pending_email  : email entered, confirmation link sent (single-use token).
--   email_verified : link clicked (OAuth intents start here — provider verifies).
--   paid           : Stripe checkout.session.completed received (authoritative).
--   completed      : account (tenant + owner user) provisioned from this intent.
--   cancelled/expired : abandoned; eligible for cleanup, email reusable.
--
-- The token threads the funnel across the email hop and the Stripe redirect.
CREATE TABLE signup_intents (
    id                          BIGSERIAL PRIMARY KEY,
    token                       TEXT NOT NULL,
    email                       TEXT NOT NULL,
    account_type                TEXT NOT NULL DEFAULT 'individual',
    plan_code                   TEXT NOT NULL DEFAULT '',
    billing_cycle               TEXT NOT NULL DEFAULT 'annual',
    status                      TEXT NOT NULL DEFAULT 'pending_email',
    -- Populated for the Google/Microsoft path (email is pre-verified there).
    oauth_provider              TEXT NOT NULL DEFAULT '',
    oauth_subject               TEXT NOT NULL DEFAULT '',
    full_name                   TEXT NOT NULL DEFAULT '',
    -- Stripe handles, filled as the funnel progresses.
    stripe_customer_id          TEXT NOT NULL DEFAULT '',
    stripe_checkout_session_id  TEXT NOT NULL DEFAULT '',
    stripe_subscription_id      TEXT NOT NULL DEFAULT '',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at                  TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours'),
    verified_at                 TIMESTAMPTZ,
    paid_at                     TIMESTAMPTZ,
    completed_at                TIMESTAMPTZ,

    CONSTRAINT signup_intents_status_chk
        CHECK (status = ANY (ARRAY[
            'pending_email'::text, 'email_verified'::text, 'paid'::text,
            'completed'::text, 'cancelled'::text, 'expired'::text])),
    CONSTRAINT signup_intents_account_type_chk
        CHECK (account_type = ANY (ARRAY['individual'::text, 'business'::text])),
    CONSTRAINT signup_intents_billing_cycle_chk
        CHECK (billing_cycle = ANY (ARRAY['monthly'::text, 'annual'::text]))
);

CREATE UNIQUE INDEX signup_intents_token_uq ON signup_intents (token);
CREATE INDEX signup_intents_email_idx ON signup_intents (email);
CREATE INDEX signup_intents_status_idx ON signup_intents (status);
-- One Stripe checkout session maps to exactly one intent (webhook lookup).
CREATE UNIQUE INDEX signup_intents_checkout_session_uq
    ON signup_intents (stripe_checkout_session_id)
    WHERE stripe_checkout_session_id <> '';
-- At most one live (pre-paid) intent per email; paid/completed/expired don't
-- block a fresh attempt, and an abandoner's email frees up once cleaned.
CREATE UNIQUE INDEX signup_intents_live_email_uq
    ON signup_intents (email)
    WHERE status IN ('pending_email', 'email_verified');
