-- Per-account monthly transfer (bandwidth) metering — the subsystem behind the
-- plans.max_transfer_bytes_month dial. One row per tenant per calendar month
-- (UTC); bytes_in = uploaded, bytes_out = downloaded. A new month is simply a
-- new row (no reset job needed). Usage for the cap check = bytes_in + bytes_out
-- for the current month. NULL max_transfer_bytes_month on the plan = unlimited.
CREATE TABLE IF NOT EXISTS transfer_usage (
    tenant_id    BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period_start DATE        NOT NULL,                 -- first day of the calendar month (UTC)
    bytes_in     BIGINT      NOT NULL DEFAULT 0,       -- uploaded (ingress)
    bytes_out    BIGINT      NOT NULL DEFAULT 0,       -- downloaded (egress)
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, period_start)
);
