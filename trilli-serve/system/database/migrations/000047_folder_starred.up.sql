-- Folders can be starred, mirroring files. NULL = not starred.
ALTER TABLE folders ADD COLUMN IF NOT EXISTS starred_at TIMESTAMPTZ;
