DROP TABLE IF EXISTS tenant_member_folders;
DROP TABLE IF EXISTS invite_folders;

ALTER TABLE invites
    ADD COLUMN IF NOT EXISTS folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL;

ALTER TABLE tenant_members
    ADD COLUMN IF NOT EXISTS root_folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL;
