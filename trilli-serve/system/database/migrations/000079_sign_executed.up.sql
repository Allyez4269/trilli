-- Trilli Sign phase 3: the executed document. On completion the snapshot is
-- flattened with every signature + field value composited in, then sealed with
-- a PKCS#7 digital signature. Both are envelope-owned encrypted blobs.
ALTER TABLE sign_envelopes ADD COLUMN IF NOT EXISTS executed_blob TEXT; -- flattened, human-visible
ALTER TABLE sign_envelopes ADD COLUMN IF NOT EXISTS sealed_blob   TEXT; -- executed + cryptographic seal
