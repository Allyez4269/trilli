-- Trash retention: keep soft-deleted files/folders restorable for a window
-- (7 days), then purge. Builds on the existing status='trashed' + deleted_at
-- columns. Adds:
--   deleted_by_user_id — who trashed it (soft pointer, no FK: a user delete
--                        must never cascade-erase trash history/attribution)
--   purge_at           — when the janitor may permanently delete it
--   trash_batch        — groups one delete operation (a folder + its whole
--                        subtree share a batch) so the trash UI shows one row
--                        per deletion and Restore brings the subtree back
--   trash_root         — true for the item the user explicitly deleted (the
--                        batch's top); the trash list shows only roots
--
-- Bytes stay in place — blobs are only removed at purge. status stays
-- active|trashed (purge hard-deletes the row, so no 'purged' state needed).

CREATE SEQUENCE IF NOT EXISTS trash_batch_seq;

ALTER TABLE files
    ADD COLUMN IF NOT EXISTS deleted_by_user_id BIGINT,
    ADD COLUMN IF NOT EXISTS purge_at           TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS trash_batch        BIGINT,
    ADD COLUMN IF NOT EXISTS trash_root         BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE folders
    ADD COLUMN IF NOT EXISTS deleted_by_user_id BIGINT,
    ADD COLUMN IF NOT EXISTS purge_at           TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS trash_batch        BIGINT,
    ADD COLUMN IF NOT EXISTS trash_root         BOOLEAN NOT NULL DEFAULT false;

-- Janitor scans: trashed rows due for purge.
CREATE INDEX IF NOT EXISTS idx_files_purge
    ON files (purge_at) WHERE status = 'trashed';
CREATE INDEX IF NOT EXISTS idx_folders_purge
    ON folders (purge_at) WHERE status = 'trashed';

-- Restore/purge resolve a whole batch by id.
CREATE INDEX IF NOT EXISTS idx_files_trash_batch
    ON files (trash_batch) WHERE trash_batch IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_folders_trash_batch
    ON folders (trash_batch) WHERE trash_batch IS NOT NULL;
