-- Trilli Sign: per-tenant preferences. Where completed (executed + sealed)
-- agreements are deposited in Files; NULLs = the account-default workspace root.
CREATE TABLE IF NOT EXISTS sign_settings (
    tenant_id    BIGINT PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    workspace_id BIGINT REFERENCES workspaces(id) ON DELETE SET NULL,
    folder_id    BIGINT REFERENCES folders(id) ON DELETE SET NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
