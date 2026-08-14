-- Reintroduce 'admin' as a valid role tier between owner and member.
-- Admins get the same access as Owner for workspace-management
-- surfaces (Users page, invites, billing read), but ownership itself
-- is still transferred-only — admin is NOT promotable-to-owner by
-- the act of being invited.
--
-- Touches both check constraints:
--   tenant_members.role  — adds admin
--   invites.role         — adds admin (so invite Create accepts it)

ALTER TABLE tenant_members
    DROP CONSTRAINT IF EXISTS tenant_members_role_chk;

ALTER TABLE tenant_members
    ADD CONSTRAINT tenant_members_role_chk
        CHECK (role = ANY (ARRAY['owner'::text, 'admin'::text, 'member'::text, 'viewer'::text]));

ALTER TABLE invites
    DROP CONSTRAINT IF EXISTS invites_role_chk;

ALTER TABLE invites
    ADD CONSTRAINT invites_role_chk
        CHECK (role = ANY (ARRAY['admin'::text, 'member'::text, 'viewer'::text]));
