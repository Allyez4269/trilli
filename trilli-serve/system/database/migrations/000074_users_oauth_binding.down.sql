DROP INDEX IF EXISTS users_oauth_identity_idx;
ALTER TABLE users DROP COLUMN IF EXISTS oauth_subject;
ALTER TABLE users DROP COLUMN IF EXISTS oauth_provider;
