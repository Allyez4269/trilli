-- Stripe Product + recurring Price ids per plan, for subscription billing.
-- Populated by `trilli stripe sync-plans`. A monthly and an annual Price per
-- plan (Stripe Prices are immutable, so a price change creates a new id here).
ALTER TABLE plans ADD COLUMN IF NOT EXISTS stripe_product_id       TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN IF NOT EXISTS stripe_price_monthly_id TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN IF NOT EXISTS stripe_price_annual_id  TEXT NOT NULL DEFAULT '';
