-- password_reset_requests backs the verified "reset your password by email"
-- flow. A logged-in user passes a step-up check (2FA code or a passkey), we
-- store a single-use token here, and email a /reset-password/<token> link.
-- Opening the link shows a "set a new password" form; submitting it (a POST,
-- never a bare GET, so email-client prefetchers can't consume it) updates the
-- password and signs the user out everywhere.
--
-- Tokens are single-use (status -> 'used') with a short 30-minute TTL.
-- Re-requesting supersedes the prior pending row rather than stacking.

CREATE TABLE password_reset_requests (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE,
    token       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    expires_at  TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 minutes'),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    used_at     TIMESTAMPTZ,

    CONSTRAINT password_reset_status_chk
        CHECK (status = ANY (ARRAY['pending'::text, 'used'::text, 'cancelled'::text, 'expired'::text]))
);

CREATE UNIQUE INDEX password_reset_token_uq
    ON password_reset_requests (token);

-- At most one outstanding request per user — a new request cancels the old.
CREATE UNIQUE INDEX password_reset_pending_per_user_uq
    ON password_reset_requests (user_id)
    WHERE status = 'pending';
