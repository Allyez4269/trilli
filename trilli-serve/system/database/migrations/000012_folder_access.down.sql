DROP INDEX IF EXISTS idx_tenant_members_root_folder;
DROP INDEX IF EXISTS idx_invites_folder;

ALTER TABLE tenant_members DROP COLUMN IF EXISTS root_folder_id;
ALTER TABLE invites DROP COLUMN IF EXISTS folder_id;
