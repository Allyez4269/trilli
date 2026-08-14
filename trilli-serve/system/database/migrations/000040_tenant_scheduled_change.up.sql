-- A pending deferred plan change (downgrade) that applies at term-end, for
-- display in the Plans/Billing UI. Set when a downgrade is scheduled via a
-- Stripe Subscription Schedule; cleared when it applies (or is superseded).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS scheduled_plan_code TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS scheduled_change_at TIMESTAMPTZ;
