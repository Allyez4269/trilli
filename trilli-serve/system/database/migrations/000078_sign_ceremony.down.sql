ALTER TABLE sign_recipients DROP COLUMN IF EXISTS signature_blob;
ALTER TABLE sign_recipients DROP COLUMN IF EXISTS signature_kind;
ALTER TABLE sign_recipients DROP COLUMN IF EXISTS consent_at;
