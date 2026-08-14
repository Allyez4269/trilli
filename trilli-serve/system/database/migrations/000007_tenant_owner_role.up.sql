-- Introduce the immutable "owner" role above "admin". The signup creator
-- of a tenant is the Owner; everyone else they invite can be Admin,
-- Editor, or Viewer. Owner is the only role that cannot be deleted, and
-- ownership is transferred (not invited).
--
-- We widen the allowed role enum to {owner, admin, editor, viewer} and
-- back-fill the oldest member of every existing tenant to owner.

ALTER TABLE tenant_members
    DROP CONSTRAINT IF EXISTS tenant_members_role_chk;

ALTER TABLE tenant_members
    ADD CONSTRAINT tenant_members_role_chk
        CHECK (role = ANY (ARRAY['owner'::text, 'admin'::text, 'editor'::text, 'viewer'::text]));

-- Backfill: promote the earliest membership of each tenant to owner.
-- For our current data this is a 1-to-1 (each tenant has one member),
-- but the GROUP BY makes it correct even if a tenant has many.
UPDATE tenant_members tm
   SET role = 'owner', updated_at = NOW()
 WHERE tm.id IN (
       SELECT MIN(id) FROM tenant_members GROUP BY tenant_id
 );
