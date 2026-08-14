-- Trilli Sign phase 1: envelopes. An envelope wraps an immutable PDF snapshot
-- (copied from the source file at creation, so later edits/deletes of the
-- source can't change what's being signed) plus recipients, placed fields, and
-- an append-only event trail that later backs the completion certificate.
CREATE TABLE IF NOT EXISTS sign_envelopes (
    id                 BIGSERIAL   PRIMARY KEY,
    tenant_id          BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    created_by_user_id BIGINT      NOT NULL REFERENCES users(id),
    title              TEXT        NOT NULL,
    message            TEXT        NOT NULL DEFAULT '',
    status             TEXT        NOT NULL DEFAULT 'draft',
    source_file_id     BIGINT,                -- provenance only; may dangle if source is deleted
    blob_path          TEXT        NOT NULL,  -- the envelope's own encrypted snapshot
    size_bytes         BIGINT      NOT NULL DEFAULT 0,
    page_count         INTEGER     NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at            TIMESTAMPTZ,
    completed_at       TIMESTAMPTZ,
    CONSTRAINT sign_envelopes_status_chk CHECK (status IN ('draft','sent','completed','voided'))
);
CREATE INDEX IF NOT EXISTS sign_envelopes_tenant_idx ON sign_envelopes (tenant_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS sign_recipients (
    id            BIGSERIAL   PRIMARY KEY,
    envelope_id   BIGINT      NOT NULL REFERENCES sign_envelopes(id) ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    email         TEXT        NOT NULL,
    signing_order INTEGER     NOT NULL DEFAULT 1,
    token         TEXT        NOT NULL UNIQUE,  -- the signer's ceremony link secret
    status        TEXT        NOT NULL DEFAULT 'pending',
    notified_at   TIMESTAMPTZ,
    viewed_at     TIMESTAMPTZ,
    signed_at     TIMESTAMPTZ,
    CONSTRAINT sign_recipients_status_chk CHECK (status IN ('pending','notified','viewed','signed','declined'))
);
CREATE INDEX IF NOT EXISTS sign_recipients_envelope_idx ON sign_recipients (envelope_id);

CREATE TABLE IF NOT EXISTS sign_fields (
    id           BIGSERIAL PRIMARY KEY,
    envelope_id  BIGINT    NOT NULL REFERENCES sign_envelopes(id) ON DELETE CASCADE,
    recipient_id BIGINT    NOT NULL REFERENCES sign_recipients(id) ON DELETE CASCADE,
    kind         TEXT      NOT NULL,
    page         INTEGER   NOT NULL,             -- 1-based
    x            REAL      NOT NULL,             -- normalized 0..1 of page width
    y            REAL      NOT NULL,             -- normalized 0..1 of page height
    w            REAL      NOT NULL,
    h            REAL      NOT NULL,
    required     BOOLEAN   NOT NULL DEFAULT TRUE,
    value        TEXT      NOT NULL DEFAULT '',  -- filled during the ceremony (phase 2)
    CONSTRAINT sign_fields_kind_chk CHECK (kind IN ('signature','initials','date','text','checkbox'))
);
CREATE INDEX IF NOT EXISTS sign_fields_envelope_idx ON sign_fields (envelope_id);

-- Append-only event trail: who did what, when — feeds the audit view and the
-- phase-4 completion certificate. Never updated or deleted while the envelope
-- lives; rows cascade only with the envelope itself.
CREATE TABLE IF NOT EXISTS sign_events (
    id          BIGSERIAL   PRIMARY KEY,
    envelope_id BIGINT      NOT NULL REFERENCES sign_envelopes(id) ON DELETE CASCADE,
    actor       TEXT        NOT NULL,  -- user email or 'system'
    action      TEXT        NOT NULL,  -- created|updated|recipient_added|sent|notified|viewed|signed|...
    detail      TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sign_events_envelope_idx ON sign_events (envelope_id, created_at);
