-- Assign a per-plan maximum individual file size. NULL = unlimited.
-- Lite: 5 GB, Plus: 25 GB, Infinity: unlimited (left NULL, matching its
-- unlimited storage/transfer). The retired placeholder keeps its 100 MB.
UPDATE plans SET max_file_size_bytes = 5368709120  WHERE code = 'lite';      -- 5 GB
UPDATE plans SET max_file_size_bytes = 26843545600 WHERE code = 'plus';      -- 25 GB
UPDATE plans SET max_file_size_bytes = NULL        WHERE code = 'infinity';  -- unlimited
