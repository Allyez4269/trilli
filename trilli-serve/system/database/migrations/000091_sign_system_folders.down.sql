ALTER TABLE folders DROP COLUMN IF EXISTS protected_source;
ALTER TABLE sign_settings DROP COLUMN IF EXISTS root_folder_id;
ALTER TABLE sign_settings DROP COLUMN IF EXISTS drafts_folder_id;
ALTER TABLE sign_settings DROP COLUMN IF EXISTS signed_folder_id;
