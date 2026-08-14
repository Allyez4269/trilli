-- Append-only audit log for the Activity page.
--
-- Every mutating (and download) action on a file or folder records one
-- row here. The log is immutable by design: a BEFORE UPDATE OR DELETE
-- trigger raises an exception so neither the app nor an operator can
-- silently rewrite history. This is what backs the "entries are
-- immutable" badge on the Activity surface — it's enforced at the
-- database, not just the UI.
--
-- actor_email is snapshotted at write time so attribution survives a
-- later user deletion (actor_user_id is a soft pointer with no FK, for
-- the same reason — we never want a user delete to cascade-erase audit
-- history). target_id is likewise a plain id, not an FK: the file or
-- folder it references may be hard-deleted later, but the event stays.

CREATE TABLE IF NOT EXISTS audit_events (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_user_id BIGINT,            -- soft pointer (no FK); NULL if the user was removed
    actor_email   TEXT NOT NULL,     -- snapshot at event time
    action        TEXT NOT NULL,     -- upload|download|move|rename|delete|restore|create_folder|share|star|view
    target_type   TEXT NOT NULL,     -- 'file' | 'folder'
    target_id     BIGINT,            -- file/folder id at event time (no FK; target may be purged)
    item_name     TEXT NOT NULL,     -- snapshot of the subject's name
    item_path     TEXT,              -- snapshot breadcrumb path ("/Documents/Reports")
    from_path     TEXT,              -- moves: source path
    to_path       TEXT,              -- moves: destination path
    old_name      TEXT,              -- renames: previous name
    device        TEXT,              -- 'Desktop' | 'Mobile' | 'App'
    ip            TEXT,              -- client IP (v4 or v6)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Primary read path: a tenant's events newest-first, paginated.
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_created
    ON audit_events (tenant_id, created_at DESC, id DESC);
-- Action-filtered reads ("show only uploads").
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_action
    ON audit_events (tenant_id, action);
-- Per-actor reads + powers per-user "Recent" (latest access events by a user).
CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_actor
    ON audit_events (tenant_id, actor_user_id);

-- Immutability guard: block any UPDATE or DELETE on the log. Retention
-- purges (Phase 5) will introduce a guarded exception via a session GUC;
-- until then the log is strictly append-only.
CREATE OR REPLACE FUNCTION audit_events_block_mutation()
RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only: % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_audit_events_immutable ON audit_events;
CREATE TRIGGER trg_audit_events_immutable
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_block_mutation();
