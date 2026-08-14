ALTER TABLE plans
    DROP COLUMN IF EXISTS max_subscriptions,
    DROP COLUMN IF EXISTS available_from,
    DROP COLUMN IF EXISTS available_until;
