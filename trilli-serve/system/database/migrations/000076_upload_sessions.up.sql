-- Resumable chunked uploads. A session tracks one in-progress large upload so a
-- dropped connection resumes from the last staged chunk instead of restarting.
-- Each chunk is encrypted (per-tenant, system/storage/encryptedstore) and staged
-- as an Azure block under blob_path; on completion the blocks are committed in
-- order into the final file (files.CompleteUpload -> persistBlob). Abandoned
-- sessions (and their staged blocks) are swept after expiry.
CREATE TABLE IF NOT EXISTS upload_sessions (
    id                BIGSERIAL   PRIMARY KEY,
    token             TEXT        NOT NULL UNIQUE,       -- opaque upload id (URL secret)
    tenant_id         BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id           BIGINT      NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    parent_folder_id  BIGINT,                            -- NULL = workspace root
    workspace_id      BIGINT      NOT NULL,              -- resolved at init
    name              TEXT        NOT NULL,
    content_type      TEXT        NOT NULL DEFAULT '',
    total_size_bytes  BIGINT      NOT NULL,
    chunk_size_bytes  BIGINT      NOT NULL,              -- plaintext bytes/chunk (multiple of 64 KiB)
    total_chunks      INTEGER     NOT NULL,
    blob_path         TEXT        NOT NULL,              -- tenants/{id}/{key}
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL,
    completed_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS upload_sessions_sweep_idx
    ON upload_sessions (expires_at) WHERE completed_at IS NULL;

-- One row per successfully-staged chunk (idempotent via the PK), so resume knows
-- exactly which indices already landed and completion can verify the full set.
CREATE TABLE IF NOT EXISTS upload_session_chunks (
    session_id   BIGINT  NOT NULL REFERENCES upload_sessions(id) ON DELETE CASCADE,
    chunk_index  INTEGER NOT NULL,
    staged_bytes BIGINT  NOT NULL,   -- plaintext bytes of this chunk (sum -> quota)
    PRIMARY KEY (session_id, chunk_index)
);
