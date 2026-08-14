-- Support ticketing: customer-raised incidents + a threaded conversation per
-- ticket. Tenant-scoped like every other feature table. A future CMS reads
-- these tables to triage tickets and post agent replies (author_type='agent').

-- Human-friendly ticket reference (TKT-001000, TKT-001001, ...). Starting the
-- sequence at 1000 keeps early numbers from advertising how new the system is.
CREATE SEQUENCE IF NOT EXISTS support_ticket_number_seq START 1000;

CREATE TABLE IF NOT EXISTS support_tickets (
    id                 BIGSERIAL   PRIMARY KEY,
    ticket_number      TEXT        NOT NULL UNIQUE
                                   DEFAULT ('TKT-' || lpad(nextval('support_ticket_number_seq')::text, 6, '0')),
    tenant_id          BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    created_by_user_id BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Snapshots so a ticket stays legible even if the user is later removed.
    requester_email    TEXT        NOT NULL,
    requester_name     TEXT        NOT NULL DEFAULT '',
    subject            TEXT        NOT NULL,
    category           TEXT        NOT NULL DEFAULT 'other'
                                   CHECK (category IN ('billing','technical','account','sharing','feature','other')),
    severity           TEXT        NOT NULL DEFAULT 'normal'
                                   CHECK (severity IN ('low','normal','high','urgent')),
    status             TEXT        NOT NULL DEFAULT 'open'
                                   CHECK (status IN ('open','pending','awaiting_customer','resolved','closed')),
    -- Bumped on every new message / status change so the list can sort by
    -- "most recently active" and the UI can show "open for N days".
    last_activity_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at        TIMESTAMPTZ,
    closed_at          TIMESTAMPTZ
);

-- List path: a tenant's tickets, most-recently-active first.
CREATE INDEX IF NOT EXISTS idx_support_tickets_tenant
    ON support_tickets (tenant_id, last_activity_at DESC, id DESC);
-- A member's own tickets.
CREATE INDEX IF NOT EXISTS idx_support_tickets_creator
    ON support_tickets (created_by_user_id, last_activity_at DESC);
-- CMS triage queue: scan open/pending tickets across tenants by severity/age.
CREATE INDEX IF NOT EXISTS idx_support_tickets_status
    ON support_tickets (status, severity, created_at);

CREATE TABLE IF NOT EXISTS support_ticket_messages (
    id             BIGSERIAL   PRIMARY KEY,
    ticket_id      BIGINT      NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    -- Denormalized for cheap tenant-scoped reads without joining the parent.
    tenant_id      BIGINT      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    author_type    TEXT        NOT NULL CHECK (author_type IN ('customer','agent','system')),
    -- NULL for agent/system messages authored from the backend (no app user).
    author_user_id BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    author_name    TEXT        NOT NULL DEFAULT '',
    body           TEXT        NOT NULL,
    -- Agent-only private notes (never shown to the customer). Reserved for the
    -- future CMS; customer-facing replies are is_internal = FALSE.
    is_internal    BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_support_ticket_messages_ticket
    ON support_ticket_messages (ticket_id, created_at, id);
