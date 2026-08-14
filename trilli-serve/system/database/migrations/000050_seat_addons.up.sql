-- Seat add-ons. Every plan includes 2 seats (the account owner + 1). Additional
-- seats are purchased one at a time at a per-plan monthly price; annual billing
-- inherits the plan's annual discount (no separate seat discount). NULL
-- per_seat_cents = seats not sold (e.g. the retired placeholder).
ALTER TABLE plans   ADD COLUMN IF NOT EXISTS per_seat_cents INT;                 -- monthly, per extra seat
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS extra_seats    INT NOT NULL DEFAULT 0;

-- Included seats = 2 across all live plans; extra seats are a flat $5/seat/mo
-- on every plan (owner decision — originally seeded per-tier at $8/$10/$12,
-- later flattened in the live catalog; seed updated to match so a fresh
-- bootstrap comes up consistent with production and the marketing site).
UPDATE plans SET max_users = 2, per_seat_cents = 500 WHERE code = 'lite';      -- $5 / seat / mo
UPDATE plans SET max_users = 2, per_seat_cents = 500 WHERE code = 'plus';      -- $5 / seat / mo
UPDATE plans SET max_users = 2, per_seat_cents = 500 WHERE code = 'infinity';  -- $5 / seat / mo
