-- Plan catalog: turn the placeholder `plans` table into a fully admin-managed
-- catalog. Adds the per-plan allowance dials (list A), pricing + presentation
-- (list C), a lifecycle status, and free-text marketing lines. Account ↔ plan
-- stays a live FK (tenants.plan_id); price is snapshot-locked on the tenant at
-- purchase (the hybrid model: allowances follow the plan live, price is frozen).
BEGIN;

-- Numeric allowances use NULL to mean "Unlimited". Existing limit columns were
-- NOT NULL; relax them so a plan can be uncapped. Reads COALESCE NULL to max
-- int64, so an uncapped plan never blocks.
ALTER TABLE plans ALTER COLUMN max_storage_bytes     DROP NOT NULL;
ALTER TABLE plans ALTER COLUMN max_users             DROP NOT NULL;
ALTER TABLE plans ALTER COLUMN max_file_size_bytes   DROP NOT NULL;
ALTER TABLE plans ALTER COLUMN max_share_expiry_days DROP NOT NULL;

-- New allowance dials (list A).
ALTER TABLE plans ADD COLUMN IF NOT EXISTS max_transfer_bytes_month BIGINT;          -- NULL = unlimited
ALTER TABLE plans ADD COLUMN IF NOT EXISTS max_workspaces           INTEGER;          -- NULL = unlimited
ALTER TABLE plans ADD COLUMN IF NOT EXISTS min_seats                INTEGER NOT NULL DEFAULT 1;
ALTER TABLE plans ADD COLUMN IF NOT EXISTS trash_retention_days     INTEGER;          -- NULL = unlimited
ALTER TABLE plans ADD COLUMN IF NOT EXISTS api_access               BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE plans ADD COLUMN IF NOT EXISTS support_level            SMALLINT NOT NULL DEFAULT 1;

-- Pricing + presentation (list C).
ALTER TABLE plans ADD COLUMN IF NOT EXISTS price_monthly_cents INTEGER  NOT NULL DEFAULT 0;
ALTER TABLE plans ADD COLUMN IF NOT EXISTS annual_discount_pct SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE plans ADD COLUMN IF NOT EXISTS tagline    TEXT    NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN IF NOT EXISTS callout    TEXT    NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN IF NOT EXISTS icon_key   TEXT    NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN IF NOT EXISTS is_popular BOOLEAN NOT NULL DEFAULT FALSE;

-- Free-text marketing bullets (static line items with no lever behind them).
ALTER TABLE plans ADD COLUMN IF NOT EXISTS marketing_lines JSONB NOT NULL DEFAULT '[]'::jsonb;
-- Forward-flex bag for future per-plan flags without a migration.
ALTER TABLE plans ADD COLUMN IF NOT EXISTS entitlements JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Lifecycle: draft (hidden) -> available (in catalog, purchasable) -> retired
-- (hidden + unpurchasable, still backs existing accounts) -> archived (deletable,
-- zero accounts). `is_active` is kept for backward compatibility with existing
-- queries and tracks "still usable as a backing plan" (available OR retired).
ALTER TABLE plans ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'draft';
ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_status_chk;
ALTER TABLE plans ADD CONSTRAINT plans_status_chk
    CHECK (status IN ('draft','available','retired','archived'));

-- Snapshot-locked price on the account (hybrid model). NULL until a plan is
-- assigned; allowances always read live from plans via plan_id.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS locked_price_cents    INTEGER;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS locked_billing_period TEXT;     -- 'monthly' | 'annual'
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS locked_at             TIMESTAMPTZ;

-- Retire the placeholder so it leaves the catalog but still backs any tenant
-- currently pointing at it (grandfathered) until they're reassigned.
UPDATE plans SET status = 'retired', is_active = FALSE WHERE code = 'default';

-- Seed the three real, available plans (Lite / Plus / Infinity). NULL = unlimited.
-- Byte values: 1 TB = 1099511627776.
INSERT INTO plans (
    code, name, icon_key, callout, tagline,
    price_monthly_cents, annual_discount_pct,
    max_storage_bytes, max_transfer_bytes_month, max_workspaces,
    max_users, min_seats, max_file_size_bytes, max_share_expiry_days,
    trash_retention_days, api_access, support_level,
    is_active, is_popular, status, sort_order
) VALUES
  ('lite', 'Lite', 'feather', 'Essential',
   'Generous transfer with a solid 3 TB to start.',
   1999, 34,
   3298534883328, 16492674416640, 2,
   NULL, 1, NULL, NULL,
   30, FALSE, 1,
   TRUE, FALSE, 'available', 1),

  ('plus', 'Plus', 'plus', 'Popular',
   'Room to grow with massive transfer.',
   2999, 34,
   7696581394432, 54975581388800, 10,
   NULL, 1, NULL, NULL,
   30, TRUE, 2,
   TRUE, TRUE, 'available', 2),

  ('infinity', 'Infinity', 'infinity', 'Great for Business',
   'No ceilings — unlimited storage and transfer.',
   5299, 34,
   NULL, NULL, NULL,
   NULL, 1, NULL, NULL,
   30, TRUE, 3,
   TRUE, FALSE, 'available', 3)
ON CONFLICT (code) DO NOTHING;

COMMIT;
