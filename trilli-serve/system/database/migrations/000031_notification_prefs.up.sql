-- Per-user email notification preferences (account-wide). Key/value rows so new
-- notification types need no migration; an absent row falls back to the code
-- default. Correlated to the user via FK.
CREATE TABLE IF NOT EXISTS user_notification_prefs (
    user_id  BIGINT  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key      TEXT    NOT NULL,
    enabled  BOOLEAN NOT NULL,
    PRIMARY KEY (user_id, key)
);

-- Consent / audit ledger: one row per "Update settings" action, recording WHEN,
-- from WHERE (ip + resolved geo), and the resulting preference snapshot. Backs
-- CAN-SPAM / GDPR / COPPA record-keeping (proof of consent + change history).
CREATE TABLE IF NOT EXISTS notification_pref_changes (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    prefs        JSONB  NOT NULL,
    ip           INET,
    country_code TEXT,
    country      TEXT,
    region       TEXT,
    city         TEXT,
    latitude     DOUBLE PRECISION,
    longitude    DOUBLE PRECISION,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_notif_pref_changes_user ON notification_pref_changes (user_id, created_at DESC);
