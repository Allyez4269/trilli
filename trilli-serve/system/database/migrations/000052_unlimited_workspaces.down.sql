-- Restore the original seeded workspace caps (see 000032_plan_catalog.up.sql).
-- Infinity stays NULL (already unlimited).
UPDATE plans SET max_workspaces = 2 WHERE code = 'lite';
UPDATE plans SET max_workspaces = 10 WHERE code = 'plus';
