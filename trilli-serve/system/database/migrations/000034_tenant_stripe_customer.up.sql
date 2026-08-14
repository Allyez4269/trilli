-- Stripe customer id per account (tenant). Holds the account's saved payment
-- methods + subscription on Stripe's side; created on first checkout.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS stripe_customer_id TEXT;
