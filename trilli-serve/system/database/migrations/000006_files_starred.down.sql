DROP INDEX IF EXISTS idx_files_tenant_starred;
ALTER TABLE files DROP COLUMN IF EXISTS starred_at;
