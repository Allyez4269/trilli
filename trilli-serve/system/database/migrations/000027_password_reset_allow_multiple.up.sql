-- Allow several concurrent pending password-reset links per user. Previously a
-- new request cancelled the prior one (one-pending-per-user), so clicking an
-- older email landed on an "invalid/expired" page. Now every unexpired link
-- works until one is used; the rest are invalidated at that point (in code) or
-- simply expire after their 30-minute TTL.

DROP INDEX IF EXISTS password_reset_pending_per_user_uq;
