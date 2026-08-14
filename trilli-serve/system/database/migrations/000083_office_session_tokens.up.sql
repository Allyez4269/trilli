-- Collaborative editing: per-participant access tokens for shared office
-- sessions. The session row is the DOCUMENT; each participant (creator or
-- joiner) gets their own token so WOPI CheckFileInfo can identify them
-- individually — Collabora then renders per-user cursors, name tags, and the
-- view list. Cascade: reaping a session invalidates every participant token.
CREATE TABLE office_session_tokens (
    token       TEXT PRIMARY KEY,
    session_key TEXT NOT NULL REFERENCES office_sessions(session_key) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_key, user_id)
);
CREATE INDEX office_session_tokens_user_idx ON office_session_tokens (user_id);
