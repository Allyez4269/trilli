-- One-time reconcile of the denormalized storage counters to the true sum of
-- file bytes. Pre-existing drift (the tenant counter had fallen behind the
-- actual file sizes) was inherited by the new workspace counter at Stage 1.
--
-- All file rows are either 'active' or 'trashed' (purge hard-deletes rows), and
-- trashed bytes count against quota until purged — so the correct usage is
-- simply the sum of size_bytes over all existing rows.

BEGIN;

UPDATE workspaces w
   SET storage_bytes_used = COALESCE(
           (SELECT SUM(f.size_bytes) FROM files f WHERE f.workspace_id = w.id), 0),
       updated_at = NOW();

UPDATE tenants t
   SET storage_bytes_used = COALESCE(
           (SELECT SUM(f.size_bytes) FROM files f WHERE f.tenant_id = t.id), 0),
       updated_at = NOW();

COMMIT;
