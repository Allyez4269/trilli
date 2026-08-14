DROP INDEX IF EXISTS idx_files_tiering;
ALTER TABLE files DROP COLUMN IF EXISTS access_tier;
ALTER TABLE files DROP COLUMN IF EXISTS last_accessed_at;
