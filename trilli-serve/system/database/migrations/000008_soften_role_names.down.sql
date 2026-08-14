-- Revert to the {owner, admin, editor, viewer} constraint from 000007.
-- "member" rows fold back to "admin" (no way to distinguish member from
-- editor in the older enum).
UPDATE tenant_members SET role = 'admin' WHERE role = 'member';

ALTER TABLE tenant_members
    DROP CONSTRAINT IF EXISTS tenant_members_role_chk;

ALTER TABLE tenant_members
    ADD CONSTRAINT tenant_members_role_chk
        CHECK (role = ANY (ARRAY['owner'::text, 'admin'::text, 'editor'::text, 'viewer'::text]));
