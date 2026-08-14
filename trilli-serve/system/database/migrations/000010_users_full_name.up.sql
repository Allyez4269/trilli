-- users gets a free-form full_name field collected during invite
-- acceptance (and editable from Profile later). Kept nullable so existing
-- users don't need a backfill; the UI titleizes the email's local part as
-- a fallback when full_name is NULL or empty.
ALTER TABLE users
    ADD COLUMN full_name TEXT;
