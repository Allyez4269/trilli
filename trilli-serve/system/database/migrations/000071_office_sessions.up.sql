-- Productivity office edit sessions + per-session working blobs.
--
-- Phase 2 moves the editor's live working file OUT of a shared local path on
-- the app server and into a hidden, per-tenant, per-session area of the
-- tenant's own encrypted blob store (tenants/{id}/.office/work/{key}/...).
-- This table is the registry for those sessions: the WOPI handler reads it to
-- serve CheckFileInfo/GetFile/PutFile for a given session key, and a janitor
-- reaps rows (and their working blobs) past expires_at — catching browser
-- closes and crashes that a purely client-driven cleanup can't.
--
-- Working blobs are pure derived/scratch data: they have NO files row, never
-- count against quota, and never appear in the file browser. They're invisible
-- to the user by construction.

CREATE TABLE office_sessions (
    session_key     TEXT PRIMARY KEY,               -- crypto-random WOPISrc id; the only credential the engine sees
    tenant_id       BIGINT NOT NULL REFERENCES tenants(id) ON UPDATE CASCADE ON DELETE CASCADE,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app             TEXT NOT NULL,                  -- "docs" | "sheets" | "slides"
    source_file_id  BIGINT,                         -- the Trilli file this session opened (NULL for a brand-new blank doc)
    working_path    TEXT NOT NULL,                  -- blob path of the live working file under tenants/{tenant}/.office/work/{key}/
    file_name       TEXT NOT NULL DEFAULT 'New document',  -- current display name (drives Save As default + download filename)
    -- session lifecycle -----------------------------------------------------
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- renewed on every WOPI Get/Put; drives the idle reap
    expires_at      TIMESTAMPTZ NOT NULL              -- hard TTL; the janitor reaps sessions past this
);

-- The janitor's reap scan: every expired session, oldest first.
CREATE INDEX office_sessions_expires_idx ON office_sessions (expires_at);
-- Per-user/per-app lookup (one active working file per app+user is the norm).
CREATE INDEX office_sessions_user_app_idx ON office_sessions (user_id, app);
