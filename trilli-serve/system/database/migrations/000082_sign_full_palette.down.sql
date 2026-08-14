ALTER TABLE sign_fields DROP COLUMN IF EXISTS meta;
ALTER TABLE sign_fields DROP COLUMN IF EXISTS value_blob;
ALTER TABLE sign_fields DROP CONSTRAINT IF EXISTS sign_fields_kind_chk;
ALTER TABLE sign_fields ADD CONSTRAINT sign_fields_kind_chk CHECK (kind IN
    ('signature','initials','date','text','checkbox',
     'date_signed','name','email','company','title'));
ALTER TABLE sign_envelopes DROP CONSTRAINT IF EXISTS sign_envelopes_status_chk;
ALTER TABLE sign_envelopes ADD CONSTRAINT sign_envelopes_status_chk CHECK
    (status IN ('draft','sent','completed','voided'));
