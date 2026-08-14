-- Plan availability & inventory (CMX Catalog, SPEC §6.3).
--
-- A plan may be unlimited (default — all columns NULL), quantity-limited
-- (max_subscriptions), and/or time-limited (available_from / available_until).
-- The public catalog + checkout hide/refuse a plan that is sold out or outside
-- its availability window. NULL means "no limit" for each dimension.
ALTER TABLE plans
    ADD COLUMN IF NOT EXISTS max_subscriptions BIGINT,
    ADD COLUMN IF NOT EXISTS available_from     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS available_until    TIMESTAMPTZ;

COMMENT ON COLUMN plans.max_subscriptions IS
  'Inventory cap: max active subscribing accounts; NULL = unlimited.';
COMMENT ON COLUMN plans.available_from IS
  'Plan is not offered/purchasable before this time; NULL = always.';
COMMENT ON COLUMN plans.available_until IS
  'Plan auto-retires from offer at/after this time; NULL = no expiry.';
