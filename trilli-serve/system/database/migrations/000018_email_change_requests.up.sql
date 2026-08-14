-- email_change_requests backs the verified "change your email" flow. A
-- logged-in user confirms their password and a new address; we store a
-- single-use token here and email a /confirm-email/<token> link to the NEW
-- address. Clicking it (a POST from the SPA, never a bare GET, so email-client
-- link prefetchers can't consume it) flips the user's email permanently.
--
-- Tokens are single-use (status -> 'confirmed') with a short 30-minute TTL.
-- Re-requesting supersedes the prior pending row rather than stacking.

CREATE TABLE email_change_requests (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE,
    new_email     TEXT NOT NULL,
    token         TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    expires_at    TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 minutes'),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    confirmed_at  TIMESTAMPTZ,

    CONSTRAINT email_change_status_chk
        CHECK (status = ANY (ARRAY['pending'::text, 'confirmed'::text, 'cancelled'::text, 'expired'::text]))
);

CREATE UNIQUE INDEX email_change_token_uq
    ON email_change_requests (token);

-- At most one outstanding request per user — a new request cancels the old.
CREATE UNIQUE INDEX email_change_pending_per_user_uq
    ON email_change_requests (user_id)
    WHERE status = 'pending';
