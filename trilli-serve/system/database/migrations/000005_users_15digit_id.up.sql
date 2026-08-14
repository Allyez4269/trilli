-- Mirror of 000004 but for users.id: switch from BIGSERIAL auto-increment to
-- caller-supplied 15-digit random values in the range
--   100_000_000_000_000 .. 999_999_999_999_999.
-- 15-digit max (10^15) still fits below JS Number.MAX_SAFE_INTEGER (~9*10^15)
-- so we can serialize user ids as JSON numbers without string-encoding.
--
-- Every FK referencing users.id is switched to ON UPDATE CASCADE so the
-- re-id of existing rows propagates without breaking referential integrity.

BEGIN;

-- 1. Add ON UPDATE CASCADE to every user_id FK.
ALTER TABLE tenant_members DROP CONSTRAINT tenant_members_user_id_fkey;
ALTER TABLE tenant_members ADD  CONSTRAINT tenant_members_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE tenant_members DROP CONSTRAINT tenant_members_invited_by_user_id_fkey;
ALTER TABLE tenant_members ADD  CONSTRAINT tenant_members_invited_by_user_id_fkey
    FOREIGN KEY (invited_by_user_id) REFERENCES users(id) ON UPDATE CASCADE;

ALTER TABLE sessions DROP CONSTRAINT sessions_user_id_fkey;
ALTER TABLE sessions ADD  CONSTRAINT sessions_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE files DROP CONSTRAINT files_uploaded_by_user_id_fkey;
ALTER TABLE files ADD  CONSTRAINT files_uploaded_by_user_id_fkey
    FOREIGN KEY (uploaded_by_user_id) REFERENCES users(id) ON UPDATE CASCADE;

ALTER TABLE folders DROP CONSTRAINT folders_created_by_user_id_fkey;
ALTER TABLE folders ADD  CONSTRAINT folders_created_by_user_id_fkey
    FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON UPDATE CASCADE;

-- 2. Re-id every user whose id is below the 15-digit floor.
DO $$
DECLARE
    u       RECORD;
    new_id  BIGINT;
BEGIN
    FOR u IN SELECT id FROM users WHERE id < 100000000000000 ORDER BY id LOOP
        LOOP
            new_id := floor(100000000000000 + random() * 899999999999999)::BIGINT;
            EXIT WHEN NOT EXISTS (SELECT 1 FROM users WHERE id = new_id);
        END LOOP;
        UPDATE users SET id = new_id WHERE id = u.id;
    END LOOP;
END $$;

-- 3. Drop the BIGSERIAL default.
ALTER TABLE users ALTER COLUMN id DROP DEFAULT;

-- 4. Drop the auto-created sequence.
DROP SEQUENCE IF EXISTS users_id_seq CASCADE;

COMMIT;
