-- Workspace-based invites.
--
-- Access is now whole-workspace (workspace_members), so member/viewer invites
-- carry the set of workspaces the invitee should be assigned to on accept,
-- mirroring the old invite_folders junction. Admin invites carry none (admins
-- reach every workspace by role).

CREATE TABLE IF NOT EXISTS invite_workspaces (
    invite_id    BIGINT NOT NULL REFERENCES invites(id)     ON DELETE CASCADE,
    workspace_id BIGINT NOT NULL REFERENCES workspaces(id)  ON DELETE CASCADE,
    PRIMARY KEY (invite_id, workspace_id)
);
CREATE INDEX IF NOT EXISTS idx_invite_workspaces_workspace ON invite_workspaces(workspace_id);
