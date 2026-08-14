-- Durable provider-identity binding on the user account.
--
-- Federated sign-in (Continue with Google / Microsoft) used to resolve an
-- existing account by EMAIL alone and log straight in. Microsoft's id_token
-- carries no email_verified claim and the app accepts any Entra UPN as the
-- address, so an attacker controlling their own Entra tenant could set a UPN to
-- a victim's Trilli email and sign in as them (when the victim had no second
-- factor). Binding the account to the provider's stable subject (Google `sub`,
-- Microsoft `oid`/`sub`) and requiring it on login closes that: an attacker's
-- token carries a different subject, so it can't match the victim's account.
ALTER TABLE users ADD COLUMN oauth_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN oauth_subject  TEXT NOT NULL DEFAULT '';

-- A given provider identity maps to at most one Trilli account.
CREATE UNIQUE INDEX users_oauth_identity_idx
    ON users (oauth_provider, oauth_subject)
    WHERE oauth_subject <> '';

-- Backfill bindings for accounts that already signed up via OAuth, so existing
-- users (especially Microsoft, which we'll otherwise refuse to auto-login
-- without a binding) keep working. The subject was recorded on the signup
-- intent at sign-up time; carry it onto the user. Password accounts have no
-- OAuth intent and stay unbound.
--
-- DISTINCT ON (oauth_provider, oauth_subject) maps each provider identity to
-- exactly ONE account, so the backfill can't create a duplicate that would
-- violate users_oauth_identity_idx; the completed/most-recent intent wins.
WITH binding AS (
    SELECT DISTINCT ON (si.oauth_provider, si.oauth_subject)
           si.oauth_provider,
           si.oauth_subject,
           u.id AS user_id
      FROM signup_intents si
      JOIN users u
        ON lower(u.email) = lower(si.email)
       AND u.oauth_subject = ''
     WHERE si.oauth_subject <> ''
     ORDER BY si.oauth_provider, si.oauth_subject, (si.status = 'completed') DESC, si.created_at DESC
)
UPDATE users u
   SET oauth_provider = b.oauth_provider,
       oauth_subject  = b.oauth_subject
  FROM binding b
 WHERE u.id = b.user_id;
