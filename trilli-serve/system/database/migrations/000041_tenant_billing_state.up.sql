-- S5 enforcement: lapse → read-only → purge lifecycle.
--
-- NOTE: the column is lifecycle_state, NOT billing_state — migration 000035
-- already uses billing_state for the billing-ADDRESS state ("NY"), so this
-- lapse state needs its own name to avoid clobbering customer addresses.
--
-- lifecycle_state is the authoritative account access level driven by billing
-- events (not the live over-quota "grace" condition, which is derived at read
-- time from usage vs the plan cap — it changes as files come and go with no
-- event to hang a stored value on):
--   'current' — normal, full access (also covers downgrade over-quota grace;
--               uploads are blocked by the existing quota guard, not by state)
--   'lapsed'  — subscription fully ended; read-only window before purge
--
-- When a subscription ends (auto-renew off at term-end, or dunning exhausted →
-- customer.subscription.deleted), the webhook sets lifecycle_state='lapsed',
-- lapsed_at=now, purge_at=now+30d. The lifecycle janitor sends warning emails
-- (tracked by purge_warn_stage) and, once purge_at passes, permanently deletes
-- the tenant's files. Re-subscribing within the window clears all of this.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS lifecycle_state  TEXT NOT NULL DEFAULT 'current';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS lapsed_at        TIMESTAMPTZ;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS purge_at         TIMESTAMPTZ;
-- Highest warning email already sent: 0=none, 1=day0, 2=day7, 3=day23, 4=day29(final).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS purge_warn_stage SMALLINT NOT NULL DEFAULT 0;

-- The janitor scans lapsed tenants by purge_at; a partial index keeps that cheap.
CREATE INDEX IF NOT EXISTS idx_tenants_lapsed_purge
    ON tenants (purge_at)
    WHERE lifecycle_state = 'lapsed';
