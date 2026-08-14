-- Drop portal upload alerts: when on, the portal owner is emailed each time a
-- 3rd party drops a file through the portal.
ALTER TABLE drop_portals ADD COLUMN IF NOT EXISTS notify_on_upload BOOLEAN NOT NULL DEFAULT FALSE;
