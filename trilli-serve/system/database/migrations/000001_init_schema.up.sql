BEGIN;

-- ---------------------------------------------------------------------------
-- plans: billing/quota tiers (Free / Pro / Enterprise / ...)
-- Concrete tier rows are seeded separately; this table just defines the shape.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS plans (
    id                       BIGSERIAL PRIMARY KEY,
    code                     TEXT        NOT NULL UNIQUE,
    name                     TEXT        NOT NULL,
    max_storage_bytes        BIGINT      NOT NULL,
    max_users                INTEGER     NOT NULL,
    max_file_size_bytes      BIGINT      NOT NULL,
    max_share_expiry_days    INTEGER     NOT NULL,
    is_active                BOOLEAN     NOT NULL DEFAULT TRUE,
    sort_order               INTEGER     NOT NULL DEFAULT 0,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- tenants: one row per customer org. Soft-tenancy: every tenant-owned row
-- elsewhere carries tenant_id. Denormalized counters (storage_bytes_used,
-- user_count) make quota progress bars a cheap read.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tenants (
    id                    BIGSERIAL PRIMARY KEY,
    name                  TEXT        NOT NULL,
    slug                  TEXT        NOT NULL UNIQUE,
    plan_id               BIGINT      NOT NULL REFERENCES plans(id),
    status                TEXT        NOT NULL DEFAULT 'active',
    storage_bytes_used    BIGINT      NOT NULL DEFAULT 0,
    user_count            INTEGER     NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT tenants_status_chk CHECK (status IN ('active','suspended','deleted'))
);

CREATE INDEX IF NOT EXISTS idx_tenants_status  ON tenants(status);
CREATE INDEX IF NOT EXISTS idx_tenants_plan_id ON tenants(plan_id);

-- ---------------------------------------------------------------------------
-- users: identity. is_superuser carries the SaaS-owner flag (see admin port).
-- A user exists once across the whole DB; tenant_members joins them to tenants.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id                  BIGSERIAL PRIMARY KEY,
    email               TEXT        NOT NULL UNIQUE,
    password_hash       TEXT        NOT NULL,
    password_algo       TEXT        NOT NULL DEFAULT 'argon2id',
    is_superuser        BOOLEAN     NOT NULL DEFAULT FALSE,
    email_verified_at   TIMESTAMPTZ,
    status              TEXT        NOT NULL DEFAULT 'active',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at       TIMESTAMPTZ,
    CONSTRAINT users_status_chk CHECK (status IN ('active','suspended','deleted'))
);

CREATE INDEX IF NOT EXISTS idx_users_status        ON users(status);
CREATE INDEX IF NOT EXISTS idx_users_is_superuser  ON users(is_superuser) WHERE is_superuser = TRUE;

-- ---------------------------------------------------------------------------
-- tenant_members: M:N between users and tenants, carrying role + status.
-- A user can belong to multiple tenants; uniqueness is (tenant_id, user_id).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tenant_members (
    id                  BIGSERIAL PRIMARY KEY,
    tenant_id           BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id             BIGINT      NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    role                TEXT        NOT NULL DEFAULT 'member',
    status              TEXT        NOT NULL DEFAULT 'active',
    invited_by_user_id  BIGINT      REFERENCES users(id),
    invited_at          TIMESTAMPTZ,
    joined_at           TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT tenant_members_role_chk   CHECK (role IN ('admin','member')),
    CONSTRAINT tenant_members_status_chk CHECK (status IN ('active','invited','suspended')),
    CONSTRAINT tenant_members_uniq       UNIQUE (tenant_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_tenant_members_user_id            ON tenant_members(user_id);
CREATE INDEX IF NOT EXISTS idx_tenant_members_tenant_status      ON tenant_members(tenant_id, status);

-- ---------------------------------------------------------------------------
-- sessions: server-side sessions keyed by an opaque random token (the cookie).
-- tenant_id is NULL for super-admin sessions (scope='super').
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT        PRIMARY KEY,
    user_id         BIGINT      NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    tenant_id       BIGINT      REFERENCES tenants(id)          ON DELETE CASCADE,
    scope           TEXT        NOT NULL,
    ip              INET,
    user_agent      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    CONSTRAINT sessions_scope_chk CHECK (scope IN ('tenant','super')),
    CONSTRAINT sessions_scope_tenant_chk CHECK (
        (scope = 'super'  AND tenant_id IS NULL) OR
        (scope = 'tenant' AND tenant_id IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id     ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at  ON sessions(expires_at);

-- ---------------------------------------------------------------------------
-- files: metadata for blobs in Azure. The bytes live at blob_path. A row never
-- exists without tenant_id. parent_folder_id is a placeholder for Phase 2's
-- folder hierarchy and stays NULL for now.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS files (
    id                  BIGSERIAL PRIMARY KEY,
    tenant_id           BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                TEXT        NOT NULL,
    size_bytes          BIGINT      NOT NULL,
    content_type        TEXT,
    blob_path           TEXT        NOT NULL,
    uploaded_by_user_id BIGINT      NOT NULL REFERENCES users(id),
    status              TEXT        NOT NULL DEFAULT 'active',
    parent_folder_id    BIGINT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ,
    CONSTRAINT files_status_chk CHECK (status IN ('active','trashed'))
);

CREATE INDEX IF NOT EXISTS idx_files_tenant_status         ON files(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_files_tenant_parent_folder  ON files(tenant_id, parent_folder_id);

COMMIT;
