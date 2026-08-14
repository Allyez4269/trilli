-- Per-user transfer metering. Mirrors transfer_usage but adds a user_id
-- dimension so a member's Home dashboard can show their OWN data transfer,
-- while admins/owners keep seeing the account-wide transfer_usage totals
-- (also used for the plan cap). Forward-only: transfer recorded before this
-- migration has no per-user rows and isn't backfilled.
CREATE TABLE IF NOT EXISTS transfer_usage_user (
    tenant_id    BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      BIGINT      NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    period_start DATE        NOT NULL,                 -- day (UTC), matches transfer_usage
    bytes_in     BIGINT      NOT NULL DEFAULT 0,       -- uploaded (ingress)
    bytes_out    BIGINT      NOT NULL DEFAULT 0,       -- downloaded (egress)
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, user_id, period_start)
);

CREATE INDEX IF NOT EXISTS idx_transfer_usage_user_lookup
    ON transfer_usage_user (tenant_id, user_id, period_start);
