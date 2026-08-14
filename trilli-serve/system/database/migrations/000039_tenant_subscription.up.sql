-- Subscription state mirrored from Stripe (kept in sync by the billing webhook).
-- Drives auto-renew (cancel_at_period_end), term-end (downgrades/lapse), and
-- the past-due/dunning UI.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS stripe_subscription_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS subscription_status    TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS current_period_end     TIMESTAMPTZ;
