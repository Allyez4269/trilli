-- Universal additional-seat price: $5/seat/mo on every plan that sells seats
-- (was $8/$10/$12 per plan in 000050_seat_addons). Seat charges read this
-- column live (inline Stripe PriceData), so no plan re-sync is needed; new
-- seat purchases pick up $5 immediately.
UPDATE plans SET per_seat_cents = 500 WHERE per_seat_cents IS NOT NULL;
