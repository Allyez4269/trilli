-- Files deposited by Trilli Sign are PROTECTED: they can't be trashed from
-- Files (only by deleting their envelope in Trilli Sign), keeping the file
-- and its envelope metadata connected.
ALTER TABLE files ADD COLUMN IF NOT EXISTS protected_source TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_envelopes ADD COLUMN IF NOT EXISTS deposited_file_id BIGINT;
