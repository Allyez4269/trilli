-- Restore the original per-plan seat prices (see 000050_seat_addons).
UPDATE plans SET per_seat_cents = 800  WHERE code = 'lite';
UPDATE plans SET per_seat_cents = 1000 WHERE code = 'plus';
UPDATE plans SET per_seat_cents = 1200 WHERE code = 'infinity';
