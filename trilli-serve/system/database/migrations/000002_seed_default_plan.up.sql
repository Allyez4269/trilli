-- Seed a single placeholder plan so signup can assign new tenants somewhere.
-- The concrete tier design (Free/Pro/Enterprise + real limits) is deliberately
-- punted; update or replace these values when tiers are designed.
BEGIN;

INSERT INTO plans (
    code, name,
    max_storage_bytes, max_users,
    max_file_size_bytes, max_share_expiry_days,
    is_active, sort_order
) VALUES (
    'default', 'Default (placeholder)',
    5368709120,         -- 5 GB
    5,                  -- 5 users per tenant
    104857600,          -- 100 MB max file size
    30,                 -- 30 days max share link expiry
    TRUE, 0
)
ON CONFLICT (code) DO NOTHING;

COMMIT;
