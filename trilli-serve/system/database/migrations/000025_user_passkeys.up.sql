-- WebAuthn passkeys (public-key credentials), per user.
--
-- A user may register multiple passkeys (phone, laptop, security key). We store
-- only PUBLIC data — the private key never leaves the user's device. Login is a
-- signature over a server challenge, so there is no shared secret.
--
-- credential_id is the raw credential handle (unique across all users) used to
-- look a passkey up at login. credential holds the full go-webauthn Credential
-- as JSON (public key, signature counter, flags, authenticator, attestation) —
-- the library needs the whole record back to validate an assertion and to
-- update the clone-detection counter. label is a user-facing device name.

CREATE TABLE IF NOT EXISTS user_passkeys (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BYTEA       NOT NULL UNIQUE,
    credential    JSONB       NOT NULL,
    label         TEXT        NOT NULL DEFAULT 'Passkey',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_user_passkeys_user ON user_passkeys(user_id);
