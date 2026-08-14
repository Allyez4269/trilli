-- invites table backs the magic-link member-onboarding flow. An Owner or
-- Member creates an invite for an email + role; the system returns a
-- /invite/<token> URL that the invitee opens to register and join the
-- tenant. Tokens are single-use (status flips to 'accepted') and have a
-- 7-day default TTL.
--
-- role is constrained to the same enum as tenant_members EXCEPT 'owner'
-- — ownership is transferred from the current Owner, never invited.

CREATE TABLE invites (
    id                       BIGSERIAL PRIMARY KEY,
    tenant_id                BIGINT NOT NULL REFERENCES tenants(id) ON UPDATE CASCADE ON DELETE CASCADE,
    email                    TEXT NOT NULL,
    role                     TEXT NOT NULL,
    token                    TEXT NOT NULL,
    created_by_user_id       BIGINT NOT NULL REFERENCES users(id) ON UPDATE CASCADE,
    status                   TEXT NOT NULL DEFAULT 'pending',
    expires_at               TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '7 days'),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at              TIMESTAMPTZ,
    accepted_by_user_id      BIGINT REFERENCES users(id) ON UPDATE CASCADE,

    CONSTRAINT invites_role_chk
        CHECK (role = ANY (ARRAY['member'::text, 'viewer'::text])),
    CONSTRAINT invites_status_chk
        CHECK (status = ANY (ARRAY['pending'::text, 'accepted'::text, 'revoked'::text, 'expired'::text]))
);

CREATE UNIQUE INDEX invites_token_uq
    ON invites (token);

CREATE INDEX idx_invites_tenant_status
    ON invites (tenant_id, status);

-- One outstanding (pending) invite per email per tenant — re-inviting an
-- email replaces the previous pending invite rather than stacking them.
CREATE UNIQUE INDEX invites_pending_per_email_uq
    ON invites (tenant_id, lower(email))
    WHERE status = 'pending';
