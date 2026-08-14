ALTER TABLE sessions
    DROP COLUMN IF EXISTS country_code,
    DROP COLUMN IF EXISTS country,
    DROP COLUMN IF EXISTS region,
    DROP COLUMN IF EXISTS city,
    DROP COLUMN IF EXISTS postal_code,
    DROP COLUMN IF EXISTS latitude,
    DROP COLUMN IF EXISTS longitude;
