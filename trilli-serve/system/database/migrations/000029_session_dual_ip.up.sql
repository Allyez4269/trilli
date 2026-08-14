-- Capture both client addresses per session. `ip` stays the authoritative
-- connecting IP (Cloudflare CF-Connecting-IP, v4 or v6); ip_v4 / ip_v6 hold the
-- client's addresses for each protocol, discovered by a post-login browser probe
-- to v4-only / v6-only echo endpoints. Geolocation is resolved from ip_v4 when
-- present, else ip_v6.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS ip_v4 INET,
    ADD COLUMN IF NOT EXISTS ip_v6 INET;
