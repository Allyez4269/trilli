-- Trilli Sign: the adopted INITIALS image (two-letter script), stamped on
-- 'initials' fields instead of the full signature.
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signature_initials_blob TEXT NOT NULL DEFAULT '';
