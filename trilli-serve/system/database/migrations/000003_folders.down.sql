BEGIN;

ALTER TABLE files DROP CONSTRAINT IF EXISTS files_parent_folder_fk;

DROP INDEX IF EXISTS idx_files_tenant_folder_status;
CREATE INDEX IF NOT EXISTS idx_files_tenant_parent_folder
    ON files (tenant_id, parent_folder_id);

DROP TABLE IF EXISTS folders;

COMMIT;
