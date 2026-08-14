-- Auto-renew preference per account. Defaults on; surfaced + toggled from the
-- Billing settings tab. The renewal engine that acts on it arrives with
-- subscriptions; until then this records the account's intent.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS auto_renew BOOLEAN NOT NULL DEFAULT true;
