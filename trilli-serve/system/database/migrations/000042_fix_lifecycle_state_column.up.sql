-- Corrective migration. Migration 000041 originally tried to add a
-- 'billing_state' column for the lapse lifecycle, but 000035 already owns
-- billing_state for the billing-ADDRESS state — so the ADD COLUMN IF NOT
-- EXISTS silently no-op'd and the lifecycle column was never created (and the
-- partial index was built over the address column). 000041 has since been
-- corrected to use lifecycle_state; this migration brings databases that
-- already applied the broken 000041 to the same state. Everything here is
-- idempotent, so a fresh install (which got 000041 right) is unaffected.

-- Drop the index if it was built over billing_state (wrong predicate column).
DROP INDEX IF EXISTS idx_tenants_lapsed_purge;

-- Create the lifecycle state column under its correct name.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS lifecycle_state TEXT NOT NULL DEFAULT 'current';

-- Recreate the janitor scan index over the correct column.
CREATE INDEX IF NOT EXISTS idx_tenants_lapsed_purge
    ON tenants (purge_at)
    WHERE lifecycle_state = 'lapsed';
