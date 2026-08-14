-- Simplify the role hierarchy to three tiers that read like English:
--
--   owner   — immutable, full control + billing. One per tenant.
--   member  — full access (everything except deleting the tenant /
--             changing billing). Default when invited.
--   viewer  — read-only.
--
-- Drops the placeholder admin/editor tiers in favor of the friendlier
-- "member" + "viewer" naming we'll show in the Invite Users modal.
-- Any pre-existing admin/editor rows fold into "member" (closest
-- equivalent: full access).

UPDATE tenant_members SET role = 'member', updated_at = NOW()
 WHERE role IN ('admin', 'editor');

ALTER TABLE tenant_members
    DROP CONSTRAINT IF EXISTS tenant_members_role_chk;

ALTER TABLE tenant_members
    ADD CONSTRAINT tenant_members_role_chk
        CHECK (role = ANY (ARRAY['owner'::text, 'member'::text, 'viewer'::text]));
