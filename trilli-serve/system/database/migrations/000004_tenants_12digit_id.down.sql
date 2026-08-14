-- Roll back: drop ON UPDATE CASCADE from the four FKs and restore the
-- BIGSERIAL default on tenants.id. We do NOT attempt to renumber existing
-- 12-digit tenants back to small integers — that would clash with whatever
-- new sequence values the rebuilt BIGSERIAL would hand out.

BEGIN;

ALTER TABLE files            DROP CONSTRAINT files_tenant_id_fkey;
ALTER TABLE files            ADD  CONSTRAINT files_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE folders          DROP CONSTRAINT folders_tenant_id_fkey;
ALTER TABLE folders          ADD  CONSTRAINT folders_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE tenant_members   DROP CONSTRAINT tenant_members_tenant_id_fkey;
ALTER TABLE tenant_members   ADD  CONSTRAINT tenant_members_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE sessions         DROP CONSTRAINT sessions_tenant_id_fkey;
ALTER TABLE sessions         ADD  CONSTRAINT sessions_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

CREATE SEQUENCE IF NOT EXISTS tenants_id_seq;
ALTER SEQUENCE tenants_id_seq OWNED BY tenants.id;
ALTER TABLE tenants ALTER COLUMN id SET DEFAULT nextval('tenants_id_seq');

-- Advance the sequence beyond the largest existing tenant id, so the next
-- BIGSERIAL insert doesn't collide.
SELECT setval('tenants_id_seq', COALESCE((SELECT MAX(id) FROM tenants), 0));

COMMIT;
