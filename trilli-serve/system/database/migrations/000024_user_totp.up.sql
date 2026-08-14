-- TOTP (authenticator-app) two-factor auth, per user.
--
-- A user has at most one TOTP enrollment. The secret is stored ENCRYPTED
-- (AES-GCM with the app key) — never in plaintext. The enrollment is pending
-- until confirmed_at is set (a code was verified); only then is 2FA active.
-- last_used_step records the most recent accepted 30s time step to block
-- replay of the same code within its window.

CREATE TABLE IF NOT EXISTS user_totp (
    user_id          BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_encrypted BYTEA       NOT NULL,
    confirmed_at     TIMESTAMPTZ,
    last_used_step   BIGINT      NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One-time recovery codes (hashed, like passwords). used_at marks consumption.
CREATE TABLE IF NOT EXISTS user_recovery_codes (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT        NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_recovery_codes_user ON user_recovery_codes(user_id);
