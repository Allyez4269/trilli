DROP TRIGGER IF EXISTS trg_cmx_operator_audit_immutable ON cmx_operator_audit;
DROP FUNCTION IF EXISTS cmx_operator_audit_block_mutation();
DROP TABLE IF EXISTS cmx_operator_audit;
DROP TABLE IF EXISTS cmx_login_events;
DROP TABLE IF EXISTS cmx_operator_geofences;
DROP TABLE IF EXISTS cmx_operator_sessions;
DROP TABLE IF EXISTS cmx_operator_recovery_codes;
DROP TABLE IF EXISTS cmx_operator_totp;
DROP TABLE IF EXISTS cmx_operators;
