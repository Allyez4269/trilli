-- Roll back the admin role tier. Any existing admin memberships are
-- folded into 'member' (closest functional equivalent) so the
-- check constraint can be reapplied without invalid rows.

UPDATE tenant_members SET role = 'member', updated_at = NOW()
 WHERE role = 'admin';

ALTER TABLE tenant_members
    DROP CONSTRAINT IF EXISTS tenant_members_role_chk;

ALTER TABLE tenant_members
    ADD CONSTRAINT tenant_members_role_chk
        CHECK (role = ANY (ARRAY['owner'::text, 'member'::text, 'viewer'::text]));

ALTER TABLE invites
    DROP CONSTRAINT IF EXISTS invites_role_chk;

ALTER TABLE invites
    ADD CONSTRAINT invites_role_chk
        CHECK (role = ANY (ARRAY['member'::text, 'viewer'::text]));
