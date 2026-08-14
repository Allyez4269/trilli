DROP INDEX IF EXISTS idx_folders_trash_batch;
DROP INDEX IF EXISTS idx_files_trash_batch;
DROP INDEX IF EXISTS idx_folders_purge;
DROP INDEX IF EXISTS idx_files_purge;

ALTER TABLE folders
    DROP COLUMN IF EXISTS trash_root,
    DROP COLUMN IF EXISTS trash_batch,
    DROP COLUMN IF EXISTS purge_at,
    DROP COLUMN IF EXISTS deleted_by_user_id;

ALTER TABLE files
    DROP COLUMN IF EXISTS trash_root,
    DROP COLUMN IF EXISTS trash_batch,
    DROP COLUMN IF EXISTS purge_at,
    DROP COLUMN IF EXISTS deleted_by_user_id;

DROP SEQUENCE IF EXISTS trash_batch_seq;
