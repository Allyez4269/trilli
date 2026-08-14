-- Sharing (Sh1): outbound shares of a file or folder. One unified primitive
-- covers both internal member shares and public "anyone-with-link" shares —
-- the grantee_type is the only thing that varies. A public link is just a
-- share with grantee_type='public' and no named grantee.
--
-- The token is the public URL secret (/s/<token>); optional password_hash and
-- expires_at gate access. expires_at is capped at create time by the plan's
-- max_share_expiry_days. revoked_at kills a share without deleting the row
-- (keeps it visible in the hub history). Inbound "shared with me" for internal
-- members is the same row read from grantee_user_id.
CREATE TABLE IF NOT EXISTS shares (
    id                 BIGSERIAL   PRIMARY KEY,
    tenant_id          BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    token              TEXT        NOT NULL UNIQUE,
    resource_type      TEXT        NOT NULL CHECK (resource_type IN ('file','folder')),
    file_id            BIGINT      REFERENCES files(id)   ON DELETE CASCADE,
    folder_id          BIGINT      REFERENCES folders(id) ON DELETE CASCADE,
    grantee_type       TEXT        NOT NULL CHECK (grantee_type IN ('member','email','public')),
    grantee_user_id    BIGINT      REFERENCES users(id) ON DELETE CASCADE,
    grantee_email      TEXT,
    capability         TEXT        NOT NULL DEFAULT 'download' CHECK (capability IN ('view','download')),
    password_hash      TEXT,
    expires_at         TIMESTAMPTZ,
    created_by_user_id BIGINT      NOT NULL REFERENCES users(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at         TIMESTAMPTZ,
    last_accessed_at   TIMESTAMPTZ,
    access_count       BIGINT      NOT NULL DEFAULT 0,
    -- exactly one resource reference, matching resource_type
    CONSTRAINT shares_resource_chk CHECK (
        (resource_type = 'file'   AND file_id   IS NOT NULL AND folder_id IS NULL) OR
        (resource_type = 'folder' AND folder_id IS NOT NULL AND file_id   IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_shares_tenant       ON shares (tenant_id)       WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_shares_grantee_user ON shares (grantee_user_id) WHERE revoked_at IS NULL;
