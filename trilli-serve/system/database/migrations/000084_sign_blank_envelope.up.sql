-- Trilli Sign: allow a document-less DRAFT envelope so "New envelope" can jump
-- straight to setup (recipients) and attach the PDF within the flow.
ALTER TABLE sign_envelopes ALTER COLUMN blob_path DROP NOT NULL;
