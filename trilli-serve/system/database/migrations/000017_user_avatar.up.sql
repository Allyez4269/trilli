-- Per-user avatar image. The bytes live in tenant-scoped blob storage (the
-- same store as files, under tenants/{tenant_id}/...); we only keep the opaque
-- blob key + a last-updated stamp here. avatar_updated_at drives a ?v= cache
-- bust on the serving URL so a new upload shows immediately. Avatars are a
-- workspace asset and intentionally NOT counted against the storage quota.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS avatar_key        TEXT,
    ADD COLUMN IF NOT EXISTS avatar_updated_at TIMESTAMPTZ;
