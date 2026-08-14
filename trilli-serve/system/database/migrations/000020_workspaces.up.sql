-- Workspaces: a new layer between the Account (tenant) and its folders/files.
-- An account (tenant) can have many workspaces; each holds its own folders/
-- files and carries a disk allocation drawn from the account's plan quota.
-- Users are assigned to one or more workspaces (whole-workspace access);
-- owners/admins see/manage all workspaces in the account by role.
--
-- This migration is intentionally ADDITIVE and SAFE: workspace_id is added
-- NULLABLE so the existing Go services (which don't reference it yet) keep
-- inserting rows unchanged. Stage 2 wires workspace_id into the services,
-- backfills any rows created in the gap, then sets NOT NULL + per-workspace
-- scoping + quota enforcement.

BEGIN;

CREATE TABLE workspaces (
    id                    BIGSERIAL PRIMARY KEY,
    tenant_id             BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                  TEXT   NOT NULL,
    disk_allocation_bytes BIGINT NOT NULL DEFAULT 0,  -- carved from the account's plan total; 0 = unset
    storage_bytes_used    BIGINT NOT NULL DEFAULT 0,
    status                TEXT   NOT NULL DEFAULT 'active',
    created_by_user_id    BIGINT REFERENCES users(id),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT workspaces_status_chk CHECK (status IN ('active','suspended','deleted')),
    CONSTRAINT workspaces_name_chk   CHECK (name <> '' AND length(name) <= 120)
);
CREATE INDEX idx_workspaces_tenant_status ON workspaces (tenant_id, status);
-- One active workspace name per account.
CREATE UNIQUE INDEX workspaces_uniq_name
    ON workspaces (tenant_id, lower(name))
    WHERE status = 'active';

-- workspace_members: which users may access which workspaces (whole-workspace).
-- Owners/admins are NOT listed here — they reach every workspace by role.
CREATE TABLE workspace_members (
    workspace_id        BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id             BIGINT NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    assigned_by_user_id BIGINT REFERENCES users(id),
    assigned_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX idx_workspace_members_user ON workspace_members (user_id);

-- The new scoping column on folders + files (nullable for now).
ALTER TABLE folders ADD COLUMN workspace_id BIGINT REFERENCES workspaces(id) ON DELETE CASCADE;
ALTER TABLE files   ADD COLUMN workspace_id BIGINT REFERENCES workspaces(id) ON DELETE CASCADE;

-- ---- Backfill: one Default Workspace per existing account ------------------

-- Allocate the Default Workspace the account's full plan quota, and seed its
-- usage from the tenant's maintained counter.
INSERT INTO workspaces (tenant_id, name, disk_allocation_bytes, storage_bytes_used, created_by_user_id)
SELECT t.id,
       'Default Workspace',
       p.max_storage_bytes,
       t.storage_bytes_used,
       (SELECT tm.user_id FROM tenant_members tm
         WHERE tm.tenant_id = t.id AND tm.role = 'owner'
         ORDER BY tm.id LIMIT 1)
  FROM tenants t
  JOIN plans p ON p.id = t.plan_id;

-- Move every existing folder + file into its account's Default Workspace
-- (each tenant has exactly one workspace at this point).
UPDATE folders f
   SET workspace_id = w.id
  FROM workspaces w
 WHERE w.tenant_id = f.tenant_id;

UPDATE files fi
   SET workspace_id = w.id
  FROM workspaces w
 WHERE w.tenant_id = fi.tenant_id;

-- Assign existing scoped members (member/viewer) to the Default Workspace, so
-- they keep access. (Whole-workspace access — broader than their old folder
-- grants, by design.)
INSERT INTO workspace_members (workspace_id, user_id)
SELECT w.id, tm.user_id
  FROM tenant_members tm
  JOIN workspaces w ON w.tenant_id = tm.tenant_id
 WHERE tm.status = 'active' AND tm.role IN ('member', 'viewer')
ON CONFLICT DO NOTHING;

CREATE INDEX idx_folders_workspace_status ON folders (workspace_id, status);
CREATE INDEX idx_files_workspace_status   ON files (workspace_id, status);

COMMIT;
