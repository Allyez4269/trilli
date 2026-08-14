-- Authenticated "buy a second account" flow: when user_id is set on a signup
-- intent, completing it provisions a NEW tenant owned by this existing user
-- (a second account) instead of creating a brand-new user. Null for the normal
-- cold-signup funnel (pay-before-account), which still creates user+tenant.
ALTER TABLE signup_intents
    ADD COLUMN user_id BIGINT REFERENCES users(id) ON DELETE CASCADE;
