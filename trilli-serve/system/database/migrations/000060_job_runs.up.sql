-- Observability for cluster-coordinated background jobs. The mutual-exclusion
-- mechanism is Postgres advisory locks (no table needed); this table just
-- records who ran each job, when, and the outcome — so you can see across the
-- cluster which node is doing the work. One row per job name (upserted).
CREATE TABLE IF NOT EXISTS job_runs (
    job              TEXT PRIMARY KEY,
    last_node        TEXT        NOT NULL DEFAULT '',
    last_started_at  TIMESTAMPTZ,
    last_finished_at TIMESTAMPTZ,
    last_ok          BOOLEAN     NOT NULL DEFAULT TRUE,
    last_note        TEXT        NOT NULL DEFAULT '',
    run_count        BIGINT      NOT NULL DEFAULT 0
);
