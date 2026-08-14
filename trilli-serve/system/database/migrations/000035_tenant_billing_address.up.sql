-- Billing address on the account (tenant), captured at checkout. Kept on our
-- side (in addition to Stripe) for invoices/records and to prefill future forms.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS billing_name        TEXT;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS billing_line1       TEXT;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS billing_line2       TEXT;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS billing_city        TEXT;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS billing_state       TEXT;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS billing_postal_code TEXT;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS billing_country     TEXT;
