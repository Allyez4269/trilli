-- Track when the "finish setting up" resume email was sent for a paid-but-
-- incomplete signup. The reminder is now DEFERRED to the janitor (sent only to
-- intents still 'paid' past a grace window), so completers never receive it;
-- this column makes the send idempotent so a sweep can't re-mail.
ALTER TABLE signup_intents ADD COLUMN IF NOT EXISTS resume_emailed_at TIMESTAMPTZ;
