-- Revert per-plan max file size back to unlimited for the paid tiers.
UPDATE plans SET max_file_size_bytes = NULL WHERE code IN ('lite', 'plus', 'infinity');
