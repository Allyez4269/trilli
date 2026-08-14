-- Add starred_at to files so a tenant can star/unstar individual files.
-- NULL = not starred. A NOT NULL value records WHEN the user starred it,
-- which we also use as the natural sort order in the Starred view.
ALTER TABLE files
    ADD COLUMN starred_at TIMESTAMPTZ;

-- Partial index keeps this cheap: only rows actually starred are indexed,
-- so listing/filtering Starred touches a small subset.
CREATE INDEX idx_files_tenant_starred
    ON files (tenant_id, starred_at DESC)
    WHERE starred_at IS NOT NULL;
