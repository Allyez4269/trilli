-- A public, non-sensitive handle for each session so the SPA can list and
-- revoke sessions WITHOUT ever seeing the session token (which is the id/PK and
-- doubles as the auth cookie). Revocation targets ref, not the token.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS ref UUID NOT NULL DEFAULT gen_random_uuid();

CREATE UNIQUE INDEX IF NOT EXISTS sessions_ref_uq ON sessions (ref);
