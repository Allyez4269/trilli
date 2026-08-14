-- Rollback: drop ON UPDATE CASCADE from user_id FKs and restore BIGSERIAL
-- default. Existing 15-digit users are NOT renumbered back; this is a
-- best-effort revert.

BEGIN;

ALTER TABLE tenant_members DROP CONSTRAINT tenant_members_user_id_fkey;
ALTER TABLE tenant_members ADD  CONSTRAINT tenant_members_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE tenant_members DROP CONSTRAINT tenant_members_invited_by_user_id_fkey;
ALTER TABLE tenant_members ADD  CONSTRAINT tenant_members_invited_by_user_id_fkey
    FOREIGN KEY (invited_by_user_id) REFERENCES users(id);

ALTER TABLE sessions DROP CONSTRAINT sessions_user_id_fkey;
ALTER TABLE sessions ADD  CONSTRAINT sessions_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE files DROP CONSTRAINT files_uploaded_by_user_id_fkey;
ALTER TABLE files ADD  CONSTRAINT files_uploaded_by_user_id_fkey
    FOREIGN KEY (uploaded_by_user_id) REFERENCES users(id);

ALTER TABLE folders DROP CONSTRAINT folders_created_by_user_id_fkey;
ALTER TABLE folders ADD  CONSTRAINT folders_created_by_user_id_fkey
    FOREIGN KEY (created_by_user_id) REFERENCES users(id);

CREATE SEQUENCE IF NOT EXISTS users_id_seq;
ALTER SEQUENCE users_id_seq OWNED BY users.id;
ALTER TABLE users ALTER COLUMN id SET DEFAULT nextval('users_id_seq');
SELECT setval('users_id_seq', COALESCE((SELECT MAX(id) FROM users), 0));

COMMIT;
