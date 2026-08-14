-- Split the user's name into first_name + last_name (captured at invite
-- acceptance, editable from Profile later). full_name is KEPT and auto-synced
-- ("first last") so existing readers — audit actor name + search, mailer
-- inviter name, the Members list — keep working without changes.
--
-- Backfill: first token of full_name -> first_name, the remainder ->
-- last_name. Single-token names land entirely in first_name.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS first_name TEXT,
    ADD COLUMN IF NOT EXISTS last_name  TEXT;

UPDATE users
   SET first_name = NULLIF(split_part(btrim(full_name), ' ', 1), ''),
       last_name  = NULLIF(
           btrim(substr(btrim(full_name),
                        length(split_part(btrim(full_name), ' ', 1)) + 1)),
           '')
 WHERE full_name IS NOT NULL
   AND btrim(full_name) <> ''
   AND first_name IS NULL
   AND last_name IS NULL;
