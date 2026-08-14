-- Multi-folder access for Member/Viewer invites.
--
-- Replaces the single folder_id columns from 000012 with junction
-- tables so each invite (and each member) can carry multiple root
-- grants. The service layer dedupes parent+child entries to the
-- topmost folder per branch before insert, so the stored set is
-- minimal and cascade enforcement can simply ancestor-check.

ALTER TABLE invites DROP COLUMN IF EXISTS folder_id;
ALTER TABLE tenant_members DROP COLUMN IF EXISTS root_folder_id;

CREATE TABLE IF NOT EXISTS invite_folders (
    invite_id BIGINT NOT NULL REFERENCES invites(id) ON DELETE CASCADE,
    folder_id BIGINT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    PRIMARY KEY (invite_id, folder_id)
);
CREATE INDEX IF NOT EXISTS idx_invite_folders_folder ON invite_folders(folder_id);

CREATE TABLE IF NOT EXISTS tenant_member_folders (
    tenant_member_id BIGINT NOT NULL REFERENCES tenant_members(id) ON DELETE CASCADE,
    folder_id        BIGINT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_member_id, folder_id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_member_folders_folder ON tenant_member_folders(folder_id);
