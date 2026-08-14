-- Stage 2b finalize: with the services now setting workspace_id on every new
-- folder/file, backfill any rows created in the Stage-1→2b gap, then make the
-- column NOT NULL. Also move root-folder name uniqueness from per-account to
-- per-workspace so two workspaces in one account can each have, say, a
-- "Documents" root folder.

BEGIN;

UPDATE folders f
   SET workspace_id = (SELECT w.id FROM workspaces w
                        WHERE w.tenant_id = f.tenant_id AND w.status = 'active'
                        ORDER BY w.created_at, w.id LIMIT 1)
 WHERE f.workspace_id IS NULL;

UPDATE files fi
   SET workspace_id = (SELECT w.id FROM workspaces w
                        WHERE w.tenant_id = fi.tenant_id AND w.status = 'active'
                        ORDER BY w.created_at, w.id LIMIT 1)
 WHERE fi.workspace_id IS NULL;

ALTER TABLE folders ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE files   ALTER COLUMN workspace_id SET NOT NULL;

DROP INDEX IF EXISTS folders_uniq_root;
CREATE UNIQUE INDEX folders_uniq_root
    ON folders (workspace_id, lower(name))
    WHERE parent_folder_id IS NULL AND status = 'active';

COMMIT;
