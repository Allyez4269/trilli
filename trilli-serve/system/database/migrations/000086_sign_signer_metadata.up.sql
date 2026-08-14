-- Trilli Sign: full signing-session metadata captured at ceremony completion,
-- shown on the Certificate of Completion (IP, GeoIP, coordinates, user agent).
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signer_ua      TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signer_city    TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signer_region  TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signer_country TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signer_lat     DOUBLE PRECISION;
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signer_lon     DOUBLE PRECISION;
