-- Owner-initiated account deletion uses the existing lapse machinery but a
-- distinct lifecycle_state = 'closing' (vs 'lapsed' for non-payment), so the
-- read-only gate, purge sweep, and warning emails treat it the same while the
-- reason stays distinguishable. No CHECK constraint exists on lifecycle_state,
-- so the new value needs no enum change.
--
-- closing_refund_cents records how much was refunded when an account younger
-- than 30 days is deleted (full money-back guarantee). It is the amount to
-- re-charge if the owner reactivates within the grace window; 0 = nothing was
-- refunded (account was older than 30 days, or had no payments).
ALTER TABLE tenants
    ADD COLUMN closing_refund_cents BIGINT NOT NULL DEFAULT 0;
