DROP INDEX IF EXISTS signup_intents_comp_idx;
ALTER TABLE signup_intents
    DROP COLUMN IF EXISTS is_comp,
    DROP COLUMN IF EXISTS comp_free_term_days,
    DROP COLUMN IF EXISTS comp_invited_by_operator_id,
    DROP COLUMN IF EXISTS comp_invited_by_email,
    DROP COLUMN IF EXISTS comp_tenant_id;
