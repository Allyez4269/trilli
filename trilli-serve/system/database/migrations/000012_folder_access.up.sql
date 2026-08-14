-- Folder-level access for Member and Viewer roles.
--
-- Model: a Member or Viewer is granted access at a single "root" folder.
-- That grant cascades to every descendant folder (and file) within it.
-- Role still drives the permission: Member = read+write, Viewer = read.
-- Owner / Admin have no root_folder_id — they see the whole workspace.
--
-- This migration only stores the grant. Cascade enforcement on file/
-- folder endpoints is a separate follow-up — for now, the schema is
-- ready for the auth middleware to start consulting it.

ALTER TABLE invites
    ADD COLUMN IF NOT EXISTS folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL;

ALTER TABLE tenant_members
    ADD COLUMN IF NOT EXISTS root_folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_invites_folder ON invites(folder_id);
CREATE INDEX IF NOT EXISTS idx_tenant_members_root_folder ON tenant_members(root_folder_id);
