-- Revert seat add-ons.
UPDATE plans SET max_users = NULL, per_seat_cents = NULL WHERE code IN ('lite', 'plus', 'infinity');
ALTER TABLE tenants DROP COLUMN IF EXISTS extra_seats;
ALTER TABLE plans   DROP COLUMN IF EXISTS per_seat_cents;
