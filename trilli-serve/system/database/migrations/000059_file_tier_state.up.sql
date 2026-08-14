-- Tier-transition state for the adaptive tiering engine.
--   tier_changed_at: when access_tier was last changed. Lets the engine respect
--     Azure minimum-retention windows (don't move Cool→Cold before 30d in Cool)
--     and avoid flapping.
--   access_count: lifetime read count (incremented on each download/preview).
--     A frequency signal complementing last_accessed_at's recency signal.
ALTER TABLE files
    ADD COLUMN IF NOT EXISTS tier_changed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS access_count BIGINT NOT NULL DEFAULT 0;
