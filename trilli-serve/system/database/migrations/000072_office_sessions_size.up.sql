-- Track the working-blob byte size on the office session row so WOPI
-- CheckFileInfo can report an accurate Size without a blob stat on every
-- engine poll. Seeded on Create (blank template / opened file size) and
-- updated on every WOPI PutFile (the engine's save). The engine SKIPS
-- GetFile when CheckFileInfo reports Size: 0, which left the canvas blank —
-- so an accurate size is load-bearing, not cosmetic.
ALTER TABLE office_sessions ADD COLUMN size_bytes BIGINT NOT NULL DEFAULT 0;
