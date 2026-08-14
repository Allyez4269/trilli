-- Unify complimentary ("ambassador") invites onto the signup_intents funnel so
-- comp and paid sign-ups share ONE registration page (/signup/complete). A comp
-- invite is a signup_intent with is_comp = TRUE: it skips payment, grants a free
-- term (comp_free_term_days; 0 = never expires) on completion, and records who
-- sent it (for the CMX ambassador registry) and the tenant it provisioned.
ALTER TABLE signup_intents
    ADD COLUMN is_comp                     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN comp_free_term_days         INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN comp_invited_by_operator_id BIGINT,
    ADD COLUMN comp_invited_by_email       TEXT NOT NULL DEFAULT '',
    ADD COLUMN comp_tenant_id              BIGINT;

-- The CMX ambassador registry lists comp invites newest-first, scoped per
-- operator for non-Global admins.
CREATE INDEX signup_intents_comp_idx ON signup_intents (comp_invited_by_operator_id, created_at DESC)
    WHERE is_comp;
