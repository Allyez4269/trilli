-- Collapse owner/editor/viewer back to the previous {admin, member}
-- enum. Owners become admins, anything below admin gets folded to member.
UPDATE tenant_members SET role = 'admin'  WHERE role = 'owner';
UPDATE tenant_members SET role = 'member' WHERE role IN ('editor', 'viewer');

ALTER TABLE tenant_members
    DROP CONSTRAINT IF EXISTS tenant_members_role_chk;

ALTER TABLE tenant_members
    ADD CONSTRAINT tenant_members_role_chk
        CHECK (role = ANY (ARRAY['admin'::text, 'member'::text]));
