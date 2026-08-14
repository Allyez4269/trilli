ALTER TABLE sign_envelopes DROP COLUMN IF EXISTS subject;
ALTER TABLE sign_fields DROP CONSTRAINT IF EXISTS sign_fields_kind_chk;
ALTER TABLE sign_fields ADD CONSTRAINT sign_fields_kind_chk CHECK (kind IN
    ('signature','initials','date','text','checkbox'));
