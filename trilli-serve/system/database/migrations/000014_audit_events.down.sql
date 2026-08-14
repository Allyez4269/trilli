DROP TRIGGER IF EXISTS trg_audit_events_immutable ON audit_events;
DROP FUNCTION IF EXISTS audit_events_block_mutation();
DROP TABLE IF EXISTS audit_events;
