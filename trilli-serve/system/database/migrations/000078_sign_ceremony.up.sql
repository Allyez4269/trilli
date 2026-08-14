-- Trilli Sign phase 2: the signing ceremony. A recipient adopts ONE signature
-- (drawn or typed, stored as an encrypted PNG blob) and consents; field values
-- land on sign_fields.value.
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signature_blob TEXT;
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signature_kind TEXT;
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS consent_at TIMESTAMPTZ;
