BEGIN;

DROP INDEX IF EXISTS folders_uniq_root;
CREATE UNIQUE INDEX folders_uniq_root
    ON folders (tenant_id, lower(name))
    WHERE parent_folder_id IS NULL AND status = 'active';

ALTER TABLE files   ALTER COLUMN workspace_id DROP NOT NULL;
ALTER TABLE folders ALTER COLUMN workspace_id DROP NOT NULL;

COMMIT;
