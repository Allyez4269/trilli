-- Record resolved geolocation on each session (from the qserve GeoIP service)
-- alongside the existing ip + user_agent. All nullable — private IPs and lookup
-- misses simply leave them empty.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS country_code TEXT,
    ADD COLUMN IF NOT EXISTS country      TEXT,
    ADD COLUMN IF NOT EXISTS region       TEXT,
    ADD COLUMN IF NOT EXISTS city         TEXT,
    ADD COLUMN IF NOT EXISTS postal_code  TEXT,
    ADD COLUMN IF NOT EXISTS latitude     DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS longitude    DOUBLE PRECISION;
