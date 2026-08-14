-- Trilli Sign UI rework: DocuSign-style field palette + envelope email subject.
ALTER TABLE sign_envelopes ADD COLUMN IF NOT EXISTS subject TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_fields DROP CONSTRAINT IF EXISTS sign_fields_kind_chk;
ALTER TABLE sign_fields ADD CONSTRAINT sign_fields_kind_chk CHECK (kind IN
    ('signature','initials','date','text','checkbox',      -- legacy kinds
     'date_signed','name','email','company','title'));      -- DocuSign-style palette
