ALTER TABLE sign_envelopes DROP COLUMN IF EXISTS executed_sha256;
ALTER TABLE sign_envelopes DROP COLUMN IF EXISTS sealed_sha256;
