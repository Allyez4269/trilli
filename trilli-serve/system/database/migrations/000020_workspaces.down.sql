BEGIN;

DROP INDEX IF EXISTS idx_files_workspace_status;
DROP INDEX IF EXISTS idx_folders_workspace_status;

ALTER TABLE files   DROP COLUMN IF EXISTS workspace_id;
ALTER TABLE folders DROP COLUMN IF EXISTS workspace_id;

DROP TABLE IF EXISTS workspace_members;
DROP TABLE IF EXISTS workspaces;

COMMIT;
