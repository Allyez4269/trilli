-- Trilli Sign: content digests of the final artifacts, recorded at execution
-- time — the durable server-side anchor for "is this file the one we sealed?"
ALTER TABLE sign_envelopes ADD COLUMN IF NOT EXISTS executed_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_envelopes ADD COLUMN IF NOT EXISTS sealed_sha256   TEXT NOT NULL DEFAULT '';
