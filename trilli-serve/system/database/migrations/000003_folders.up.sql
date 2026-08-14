BEGIN;

-- ---------------------------------------------------------------------------
-- folders: hierarchical containers for files. parent_folder_id NULL means
-- the folder lives at the tenant's root. Every folder is tenant-scoped.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS folders (
    id                  BIGSERIAL PRIMARY KEY,
    tenant_id           BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    parent_folder_id    BIGINT      REFERENCES folders(id) ON DELETE CASCADE,
    name                TEXT        NOT NULL,
    created_by_user_id  BIGINT      NOT NULL REFERENCES users(id),
    status              TEXT        NOT NULL DEFAULT 'active',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ,
    CONSTRAINT folders_status_chk     CHECK (status IN ('active','trashed')),
    CONSTRAINT folders_name_chk       CHECK (name <> '' AND length(name) <= 255),
    CONSTRAINT folders_no_self_parent CHECK (parent_folder_id IS NULL OR parent_folder_id <> id)
);

-- Uniqueness of folder names within a parent, restricted to active rows.
-- Two partial indexes because postgres unique constraints treat NULL parents
-- as never-equal, so a single composite UNIQUE would allow duplicate root
-- folders.
CREATE UNIQUE INDEX folders_uniq_root
    ON folders (tenant_id, lower(name))
    WHERE parent_folder_id IS NULL AND status = 'active';

CREATE UNIQUE INDEX folders_uniq_child
    ON folders (tenant_id, parent_folder_id, lower(name))
    WHERE parent_folder_id IS NOT NULL AND status = 'active';

CREATE INDEX idx_folders_tenant_parent_status
    ON folders (tenant_id, parent_folder_id, status);

-- ---------------------------------------------------------------------------
-- Tie files.parent_folder_id (added as a placeholder in 000001) to folders.
-- ON DELETE SET NULL: if a folder is hard-deleted, its files float up to the
-- tenant root rather than vanishing with the folder.
-- ---------------------------------------------------------------------------
ALTER TABLE files
    ADD CONSTRAINT files_parent_folder_fk
    FOREIGN KEY (parent_folder_id) REFERENCES folders(id) ON DELETE SET NULL;

-- Replace the looser index from 000001 with one that includes status, since
-- every browse query filters status = 'active'.
DROP INDEX IF EXISTS idx_files_tenant_parent_folder;
CREATE INDEX idx_files_tenant_folder_status
    ON files (tenant_id, parent_folder_id, status);

COMMIT;
