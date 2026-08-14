-- Trilli Sign: full DocuSeal-style field palette (no payment).
-- meta: per-field sender config — dropdown options, radio group, formula, etc.
-- value_blob: encrypted blob path for signer file attachments.
ALTER TABLE sign_fields ADD COLUMN IF NOT EXISTS meta JSONB NOT NULL DEFAULT '{}';
ALTER TABLE sign_fields ADD COLUMN IF NOT EXISTS value_blob TEXT;
ALTER TABLE sign_fields DROP CONSTRAINT IF EXISTS sign_fields_kind_chk;
ALTER TABLE sign_fields ADD CONSTRAINT sign_fields_kind_chk CHECK (kind IN
    ('signature','initials','date','text','checkbox',
     'date_signed','name','email','company','title',
     'number','dropdown','radio','note','approve','decline','attachment','formula'));
-- a declined envelope is terminal, distinct from voided-by-sender
ALTER TABLE sign_envelopes DROP CONSTRAINT IF EXISTS sign_envelopes_status_chk;
ALTER TABLE sign_envelopes ADD CONSTRAINT sign_envelopes_status_chk CHECK
    (status IN ('draft','sent','completed','voided','declined'));
