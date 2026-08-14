-- Per-tenant storage cap override (CMX Accounts → quota override, SPEC §6.2).
--
-- Lets an operator bump (or reduce) a single tenant's storage cap WITHOUT
-- changing its plan. NULL = no override; the plan's max_storage_bytes applies
-- as before. The capacity checks in system/files and system/workspaces honor
-- this via COALESCE(t.quota_override_bytes, p.max_storage_bytes, <unlimited>).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS quota_override_bytes BIGINT;

COMMENT ON COLUMN tenants.quota_override_bytes IS
  'CMX operator storage-cap override in bytes; NULL = use plan max_storage_bytes.';
