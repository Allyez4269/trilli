-- Reverse the plan catalog. Drops the seeded plans (only if no tenant references
-- them), removes the added columns, and restores the placeholder as active.
BEGIN;

-- Only delete seeded plans that nothing points at, to respect the FK.
DELETE FROM plans p
 WHERE p.code IN ('lite','plus','infinity')
   AND NOT EXISTS (SELECT 1 FROM tenants t WHERE t.plan_id = p.id);

UPDATE plans SET status = 'available', is_active = TRUE WHERE code = 'default';

ALTER TABLE tenants DROP COLUMN IF EXISTS locked_price_cents;
ALTER TABLE tenants DROP COLUMN IF EXISTS locked_billing_period;
ALTER TABLE tenants DROP COLUMN IF EXISTS locked_at;

ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_status_chk;
ALTER TABLE plans DROP COLUMN IF EXISTS status;
ALTER TABLE plans DROP COLUMN IF EXISTS entitlements;
ALTER TABLE plans DROP COLUMN IF EXISTS marketing_lines;
ALTER TABLE plans DROP COLUMN IF EXISTS is_popular;
ALTER TABLE plans DROP COLUMN IF EXISTS icon_key;
ALTER TABLE plans DROP COLUMN IF EXISTS callout;
ALTER TABLE plans DROP COLUMN IF EXISTS tagline;
ALTER TABLE plans DROP COLUMN IF EXISTS annual_discount_pct;
ALTER TABLE plans DROP COLUMN IF EXISTS price_monthly_cents;
ALTER TABLE plans DROP COLUMN IF EXISTS support_level;
ALTER TABLE plans DROP COLUMN IF EXISTS api_access;
ALTER TABLE plans DROP COLUMN IF EXISTS trash_retention_days;
ALTER TABLE plans DROP COLUMN IF EXISTS min_seats;
ALTER TABLE plans DROP COLUMN IF EXISTS max_workspaces;
ALTER TABLE plans DROP COLUMN IF EXISTS max_transfer_bytes_month;

-- Note: the DROP NOT NULL relaxations are intentionally left in place; re-adding
-- NOT NULL could fail if any uncapped (NULL) plan rows remain.

COMMIT;
