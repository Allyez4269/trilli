-- Trilli Sign system directory: "Trilli Sign" with "Signed Agreements" and
-- "Drafts" subfolders, auto-provisioned per tenant. Folders carry a
-- protected_source marker like files; ids are pinned in sign_settings.
ALTER TABLE folders ADD COLUMN IF NOT EXISTS protected_source TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_settings ADD COLUMN IF NOT EXISTS root_folder_id   BIGINT REFERENCES folders(id) ON DELETE SET NULL;
ALTER TABLE sign_settings ADD COLUMN IF NOT EXISTS drafts_folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL;
ALTER TABLE sign_settings ADD COLUMN IF NOT EXISTS signed_folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL;
