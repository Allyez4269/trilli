-- CMX operator authentication & audit schema (SPEC §4.1-B, §6.9).
--
-- Operators are the staff who run CMX. They live in CMX-owned tables, NOT the
-- app's `users` table, for trust-domain separation: a bug in customer auth can
-- never escalate to god-mode (SPEC §9). Every object here is prefixed `cmx_`.

-- ---------------------------------------------------------------------------
-- cmx_operators — operator accounts (Global admin ⊃ CMX admin, SPEC §3).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_operators (
    id                 BIGSERIAL PRIMARY KEY,
    email              TEXT NOT NULL,
    name               TEXT NOT NULL DEFAULT '',
    password_hash      TEXT NOT NULL,
    password_algo      TEXT NOT NULL DEFAULT 'argon2id',
    -- role: 'global' (super admin, full purview) | 'cmx' (tenant operator).
    role               TEXT NOT NULL DEFAULT 'cmx'
                         CHECK (role IN ('global', 'cmx')),
    -- status: 'active' | 'suspended' (admin-disabled) | 'locked' (3-strike).
    status             TEXT NOT NULL DEFAULT 'active'
                         CHECK (status IN ('active', 'suspended', 'locked')),
    failed_login_count INT  NOT NULL DEFAULT 0,
    locked_at          TIMESTAMPTZ,
    -- Per-operator geo-fence master switch (SPEC §6.9). When false, the rows in
    -- cmx_operator_geofences are ignored and the operator may log in anywhere.
    geofence_enabled   BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at      TIMESTAMPTZ,
    created_by         BIGINT,          -- cmx_operators.id of the creator (soft ref)
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Email is the login identifier — unique, case-insensitive.
CREATE UNIQUE INDEX IF NOT EXISTS ux_cmx_operators_email
    ON cmx_operators (lower(email));
CREATE INDEX IF NOT EXISTS ix_cmx_operators_role ON cmx_operators (role);

-- ---------------------------------------------------------------------------
-- cmx_operator_totp — mandatory authenticator-app 2FA (SPEC §6.9).
-- Mirrors the app's user_totp. Secret is AES-256-GCM encrypted at rest.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_operator_totp (
    operator_id      BIGINT PRIMARY KEY
                       REFERENCES cmx_operators(id) ON DELETE CASCADE,
    secret_encrypted BYTEA NOT NULL,
    confirmed_at     TIMESTAMPTZ,          -- NULL while enrollment is pending
    last_used_step   BIGINT NOT NULL DEFAULT 0,  -- TOTP replay guard
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- cmx_operator_recovery_codes — one-time recovery codes (hashed).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_operator_recovery_codes (
    id          BIGSERIAL PRIMARY KEY,
    operator_id BIGINT NOT NULL REFERENCES cmx_operators(id) ON DELETE CASCADE,
    code_hash   TEXT NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_cmx_recovery_operator
    ON cmx_operator_recovery_codes (operator_id);

-- ---------------------------------------------------------------------------
-- cmx_operator_sessions — opaque server-side sessions (SPEC §6.9):
-- 15-min idle timeout + 12-hour hard cap, enforced in code via last_seen_at /
-- created_at + expires_at. Geo columns snapshot the qserve lookup at login.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_operator_sessions (
    id              TEXT PRIMARY KEY,     -- opaque random token (cookie value)
    operator_id     BIGINT NOT NULL REFERENCES cmx_operators(id) ON DELETE CASCADE,
    ip              TEXT NOT NULL DEFAULT '',
    continent_code  TEXT NOT NULL DEFAULT '',
    country_code    TEXT NOT NULL DEFAULT '',
    region          TEXT NOT NULL DEFAULT '',
    user_agent      TEXT NOT NULL DEFAULT '',
    -- step_up_at: timestamp of the most recent fresh-2FA confirmation, used to
    -- gate consequential actions (SPEC §6.9 step-up re-auth).
    step_up_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- anchors the 12h hard cap
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- anchors the 15m idle window
    expires_at      TIMESTAMPTZ NOT NULL,                -- hard cap (created_at + 12h)
    revoked_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_cmx_sessions_operator
    ON cmx_operator_sessions (operator_id);

-- ---------------------------------------------------------------------------
-- cmx_operator_geofences — per-operator allowed login regions (SPEC §6.9).
-- Empty set (or geofence_enabled = false) means unrestricted. Otherwise a
-- login is permitted only if its resolved region matches an enabled row.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_operator_geofences (
    id          BIGSERIAL PRIMARY KEY,
    operator_id BIGINT NOT NULL REFERENCES cmx_operators(id) ON DELETE CASCADE,
    -- region_type: 'continent' (e.g. 'EU','NA') | 'country' (ISO-2, e.g. 'US').
    region_type TEXT NOT NULL CHECK (region_type IN ('continent', 'country')),
    region_code TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (operator_id, region_type, region_code)
);
CREATE INDEX IF NOT EXISTS ix_cmx_geofences_operator
    ON cmx_operator_geofences (operator_id);

-- ---------------------------------------------------------------------------
-- cmx_login_events — append-only operator login audit (SPEC §6.9). Records
-- EVERY attempt (success + failure) with IP/geo/UA. operator_id is nullable
-- for attempts against an unknown email.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_login_events (
    id              BIGSERIAL PRIMARY KEY,
    operator_id     BIGINT,               -- soft ref; NULL for unknown email
    email_attempted TEXT NOT NULL DEFAULT '',
    -- outcome: success | bad_password | locked_out | twofa_fail | geo_blocked
    --        | suspended | unknown_email
    outcome         TEXT NOT NULL,
    ip              TEXT NOT NULL DEFAULT '',
    continent_code  TEXT NOT NULL DEFAULT '',
    country_code    TEXT NOT NULL DEFAULT '',
    region          TEXT NOT NULL DEFAULT '',
    user_agent      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_cmx_login_events_operator
    ON cmx_login_events (operator_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_cmx_login_events_time
    ON cmx_login_events (created_at DESC);

-- ---------------------------------------------------------------------------
-- cmx_operator_audit — append-only operator-ACTION log (SPEC §6.7). Immutable
-- by a DB trigger (mirrors the app's audit_events): neither the app nor an
-- operator can rewrite history. Global admins see all rows; CMX admins see
-- only their own (enforced in the read query, SPEC §6.7 scoping).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cmx_operator_audit (
    id             BIGSERIAL PRIMARY KEY,
    operator_id    BIGINT NOT NULL,       -- soft ref (survives operator deletion)
    operator_email TEXT NOT NULL DEFAULT '',  -- snapshot for durable attribution
    role_snapshot  TEXT NOT NULL DEFAULT '',
    action         TEXT NOT NULL,
    target_type    TEXT NOT NULL DEFAULT '',
    target_id      TEXT NOT NULL DEFAULT '',
    tenant_id      BIGINT,                 -- nullable; set when action is tenant-scoped
    summary        TEXT NOT NULL DEFAULT '',
    meta           JSONB NOT NULL DEFAULT '{}'::jsonb,  -- sanitized detail
    ip             TEXT NOT NULL DEFAULT '',
    continent_code TEXT NOT NULL DEFAULT '',
    country_code   TEXT NOT NULL DEFAULT '',
    region         TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_cmx_audit_operator
    ON cmx_operator_audit (operator_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_cmx_audit_time
    ON cmx_operator_audit (created_at DESC);
CREATE INDEX IF NOT EXISTS ix_cmx_audit_tenant
    ON cmx_operator_audit (tenant_id) WHERE tenant_id IS NOT NULL;

-- Append-only enforcement: block UPDATE/DELETE at the database level.
CREATE OR REPLACE FUNCTION cmx_operator_audit_block_mutation()
RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'cmx_operator_audit is append-only: % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_cmx_operator_audit_immutable ON cmx_operator_audit;
CREATE TRIGGER trg_cmx_operator_audit_immutable
    BEFORE UPDATE OR DELETE ON cmx_operator_audit
    FOR EACH ROW EXECUTE FUNCTION cmx_operator_audit_block_mutation();
