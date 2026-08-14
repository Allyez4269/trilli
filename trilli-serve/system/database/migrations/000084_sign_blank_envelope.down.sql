UPDATE sign_envelopes SET blob_path = '' WHERE blob_path IS NULL;
ALTER TABLE sign_envelopes ALTER COLUMN blob_path SET NOT NULL;
