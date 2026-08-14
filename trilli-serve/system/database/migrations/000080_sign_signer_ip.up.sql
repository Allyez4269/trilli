-- Trilli Sign phase 4: record the signer's IP at completion for the audit trail
-- and the Certificate of Completion.
ALTER TABLE sign_recipients ADD COLUMN IF NOT EXISTS signer_ip TEXT;
