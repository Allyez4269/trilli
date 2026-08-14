-- No-op down: lifecycle_state and the index are owned by 000041's down. This
-- corrective migration intentionally leaves nothing extra to undo (rolling
-- back to before 000041 drops lifecycle_state there).
SELECT 1;
