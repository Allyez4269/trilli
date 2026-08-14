DROP INDEX IF EXISTS idx_tenants_lapsed_purge;
ALTER TABLE tenants DROP COLUMN IF EXISTS purge_warn_stage;
ALTER TABLE tenants DROP COLUMN IF EXISTS purge_at;
ALTER TABLE tenants DROP COLUMN IF EXISTS lapsed_at;
-- Drop lifecycle_state only — billing_state is the unrelated address column
-- owned by migration 000035 and must survive a rollback of this migration.
ALTER TABLE tenants DROP COLUMN IF EXISTS lifecycle_state;
