-- Per-file access tracking, the instrumentation the adaptive storage-tiering
-- engine is built on. last_accessed_at drives the cold-tail analysis (how much
-- data hasn't been read in 30/90 days); access_tier records which Azure tier a
-- blob currently sits in (everything is Hot today — we've never set a tier).

ALTER TABLE files
    ADD COLUMN IF NOT EXISTS last_accessed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS access_tier TEXT NOT NULL DEFAULT 'hot'
        CHECK (access_tier IN ('hot', 'cool', 'cold'));

-- Seed last_accessed_at with the best signal we already have: the most recent
-- logged download for the file, falling back to its last modification. Real
-- reads refine this forward. Downloads are the strongest "this file is hot"
-- signal we've been recording (inline previews weren't logged, so updated_at
-- backstops files that were uploaded but never explicitly downloaded).
UPDATE files f
   SET last_accessed_at = COALESCE(
       (SELECT MAX(a.created_at)
          FROM audit_events a
         WHERE a.target_type = 'file'
           AND a.target_id = f.id
           AND a.action = 'download'),
       f.updated_at)
 WHERE f.last_accessed_at IS NULL;

-- Scan path for the future tiering janitor: active files in a tenant ordered by
-- how cold they are.
CREATE INDEX IF NOT EXISTS idx_files_tiering
    ON files (tenant_id, last_accessed_at)
    WHERE status = 'active';
