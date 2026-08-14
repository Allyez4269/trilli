package trash

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"trilli/system/database/postgres"
	"trilli/system/logging"
	"trilli/system/storage"
)

// Service reads the trash, restores batches, and purges them.
type Service struct {
	client *postgres.Client
	store  storage.Store
}

func NewService(c *postgres.Client, s storage.Store) *Service {
	return &Service{client: c, store: s}
}

// ListRoots returns the trash entries (batch roots) for a tenant, newest
// deletion first. A non-nil scope restricts to entries whose original parent
// folder is in the accessible set.
func (s *Service) ListRoots(ctx context.Context, tenantID int64, scope *Scope) ([]*Entry, error) {
	folderClause, folderArgs := scopeClause(scope, "f.parent_folder_id", 2)
	// Folder roots, with subtree file size + count from the shared batch.
	folderSQL := `
		SELECT f.trash_batch, f.id, f.name, f.parent_folder_id, f.deleted_at, f.purge_at,
		       COALESCE(u.email, ''),
		       COALESCE((SELECT SUM(x.size_bytes) FROM files x WHERE x.trash_batch = f.trash_batch), 0),
		       COALESCE((SELECT COUNT(*) FROM files x WHERE x.trash_batch = f.trash_batch), 0)
		  FROM folders f
		  LEFT JOIN users u ON u.id = f.deleted_by_user_id
		 WHERE f.tenant_id = $1 AND f.status = 'trashed' AND f.trash_root AND f.trash_batch IS NOT NULL` + folderClause
	frows, err := s.client.QueryContext(ctx, folderSQL, append([]any{tenantID}, folderArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("trash: list folders: %w", err)
	}
	out := make([]*Entry, 0)
	for frows.Next() {
		var (
			e        Entry
			parent   *int64
			count    int
		)
		if err := frows.Scan(&e.Batch, &e.ID, &e.Name, &parent, &e.DeletedAt, &e.PurgeAt,
			&e.DeletedByEmail, &e.SizeBytes, &count); err != nil {
			frows.Close()
			return nil, fmt.Errorf("trash: scan folder: %w", err)
		}
		e.Kind = KindFolder
		e.ItemCount = count
		e.Location = s.pathOf(ctx, tenantID, parent)
		out = append(out, &e)
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return nil, fmt.Errorf("trash: folder rows: %w", err)
	}

	fileClause, fileArgs := scopeClause(scope, "f.parent_folder_id", 2)
	fileSQL := `
		SELECT f.trash_batch, f.id, f.name, f.parent_folder_id, f.size_bytes, f.deleted_at, f.purge_at,
		       COALESCE(u.email, '')
		  FROM files f
		  LEFT JOIN users u ON u.id = f.deleted_by_user_id
		 WHERE f.tenant_id = $1 AND f.status = 'trashed' AND f.trash_root AND f.trash_batch IS NOT NULL` + fileClause
	rows, err := s.client.QueryContext(ctx, fileSQL, append([]any{tenantID}, fileArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("trash: list files: %w", err)
	}
	for rows.Next() {
		var (
			e      Entry
			parent *int64
		)
		if err := rows.Scan(&e.Batch, &e.ID, &e.Name, &parent, &e.SizeBytes, &e.DeletedAt, &e.PurgeAt,
			&e.DeletedByEmail); err != nil {
			rows.Close()
			return nil, fmt.Errorf("trash: scan file: %w", err)
		}
		e.Kind = KindFile
		e.Location = s.pathOf(ctx, tenantID, parent)
		out = append(out, &e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("trash: file rows: %w", err)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].DeletedAt.After(out[j].DeletedAt) })
	return out, nil
}

// CountRoots returns how many trash entries (batch roots) a tenant has,
// folder-access scoped the same way as ListRoots. Cheap — no path/size work.
// Powers the sidebar count badge.
func (s *Service) CountRoots(ctx context.Context, tenantID int64, scope *Scope) (int, error) {
	total := 0
	for _, table := range []string{"folders", "files"} {
		clause, args := scopeClause(scope, "f.parent_folder_id", 2)
		var n int
		q := fmt.Sprintf(`
			SELECT COUNT(*) FROM %s f
			 WHERE f.tenant_id = $1 AND f.status = 'trashed' AND f.trash_root
			   AND f.trash_batch IS NOT NULL%s`, table, clause)
		if err := s.client.QueryRowContext(ctx, q, append([]any{tenantID}, args...)...).Scan(&n); err != nil {
			return 0, fmt.Errorf("trash: count %s: %w", table, err)
		}
		total += n
	}
	return total, nil
}

// BatchRoot returns the root identity of a batch for access gating. Returns
// ErrBatchNotFound if the batch has no trashed root in this tenant.
func (s *Service) BatchRoot(ctx context.Context, tenantID, batch int64) (*RootRef, error) {
	var r RootRef
	err := s.client.QueryRowContext(ctx, `
		SELECT 'folder', id, name, parent_folder_id, workspace_id FROM folders
		 WHERE tenant_id = $1 AND trash_batch = $2 AND trash_root AND status = 'trashed'
		UNION ALL
		SELECT 'file', id, name, parent_folder_id, workspace_id FROM files
		 WHERE tenant_id = $1 AND trash_batch = $2 AND trash_root AND status = 'trashed'
		 LIMIT 1`, tenantID, batch).Scan(&r.Kind, &r.ID, &r.Name, &r.ParentFolderID, &r.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBatchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("trash: batch root: %w", err)
	}
	return &r, nil
}

// Restore un-trashes an entire batch back to active, returning the root to
// its original location. If that location no longer exists (the parent was
// purged), the root is restored at the tenant root. A name collision at the
// destination is resolved by appending " (restored)". Returns the restored
// root for the caller to log.
func (s *Service) Restore(ctx context.Context, tenantID, batch int64) (*RootRef, error) {
	root, err := s.BatchRoot(ctx, tenantID, batch)
	if err != nil {
		return nil, err
	}

	// Resolve the destination parent: keep the original if it's active again,
	// else fall back to the tenant root.
	target := root.ParentFolderID
	if target != nil {
		var active bool
		if err := s.client.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM folders WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
			*target, tenantID).Scan(&active); err != nil {
			return nil, fmt.Errorf("trash: check restore parent: %w", err)
		}
		if !active {
			target = nil
		}
	}
	finalName, err := s.resolveRestoreName(ctx, tenantID, root, target)
	if err != nil {
		return nil, err
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("trash: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Redirect the root to its destination + resolved name FIRST, while it's
	// still trashed — the name-uniqueness indexes are WHERE status='active',
	// so a clashing name can't collide until the row goes active. Doing this
	// before reactivation guarantees the root is conflict-free when it flips.
	rootTable := "files"
	if root.Kind == KindFolder {
		rootTable = "folders"
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET parent_folder_id = $1, name = $2, updated_at = NOW()
		 WHERE id = $3 AND tenant_id = $4`, rootTable),
		target, finalName, root.ID, tenantID); err != nil {
		return nil, fmt.Errorf("trash: redirect root: %w", err)
	}

	// Now reactivate every row in the batch (root + cascade).
	for _, table := range []string{"folders", "files"} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			UPDATE %s
			   SET status = 'active', deleted_at = NULL, purge_at = NULL,
			       deleted_by_user_id = NULL, trash_batch = NULL, trash_root = false,
			       updated_at = NOW()
			 WHERE trash_batch = $1 AND tenant_id = $2`, table), batch, tenantID); err != nil {
			return nil, fmt.Errorf("trash: reactivate %s: %w", table, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("trash: commit restore: %w", err)
	}
	committed = true

	root.Name = finalName
	root.ParentFolderID = target
	logging.Info(packageName, "Restore: tenant=%d batch=%d kind=%s root=%d", tenantID, batch, root.Kind, root.ID)
	return root, nil
}

// PurgeBatch permanently removes a batch: deletes its files' blobs, frees the
// tenant's quota by the freed bytes, and hard-deletes the rows. Best-effort
// on blobs — a failed blob delete is logged, not fatal.
func (s *Service) PurgeBatch(ctx context.Context, tenantID, batch int64) error {
	// Gather blob paths + total size before deleting rows, accumulating the
	// freed bytes per workspace so we can decrement each workspace's usage.
	rows, err := s.client.QueryContext(ctx,
		`SELECT blob_path, size_bytes, workspace_id FROM files WHERE tenant_id = $1 AND trash_batch = $2`,
		tenantID, batch)
	if err != nil {
		return fmt.Errorf("trash: gather blobs: %w", err)
	}
	var blobs []string
	var freed int64
	freedByWorkspace := make(map[int64]int64)
	for rows.Next() {
		var path string
		var size int64
		var wsID sql.NullInt64
		if err := rows.Scan(&path, &size, &wsID); err != nil {
			rows.Close()
			return fmt.Errorf("trash: scan blob: %w", err)
		}
		blobs = append(blobs, path)
		freed += size
		if wsID.Valid {
			freedByWorkspace[wsID.Int64] += size
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("trash: blob rows: %w", err)
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("trash: begin purge tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM files WHERE tenant_id = $1 AND trash_batch = $2`, tenantID, batch); err != nil {
		return fmt.Errorf("trash: delete files: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM folders WHERE tenant_id = $1 AND trash_batch = $2`, tenantID, batch); err != nil {
		return fmt.Errorf("trash: delete folders: %w", err)
	}
	if freed > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE tenants SET storage_bytes_used = GREATEST(0, storage_bytes_used - $1), updated_at = NOW()
			 WHERE id = $2`, freed, tenantID); err != nil {
			return fmt.Errorf("trash: free quota: %w", err)
		}
	}
	for wsID, bytes := range freedByWorkspace {
		if bytes <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE workspaces SET storage_bytes_used = GREATEST(0, storage_bytes_used - $1), updated_at = NOW()
			 WHERE id = $2`, bytes, wsID); err != nil {
			return fmt.Errorf("trash: free workspace quota: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("trash: commit purge: %w", err)
	}
	committed = true

	// Rows are gone; now drop the bytes. Orphaned blobs (if this fails) are
	// only a storage cost and are logged for a later sweep.
	for _, path := range blobs {
		if err := s.store.Delete(ctx, path); err != nil {
			logging.Error(packageName, "Purge: blob delete failed (orphaned) path=%s: %v", path, err)
		}
	}
	logging.Info(packageName, "PurgeBatch: tenant=%d batch=%d blobs=%d freed=%d", tenantID, batch, len(blobs), freed)
	return nil
}

// PurgeExpired purges every batch whose purge_at has passed, across all
// tenants. Returns the number of batches purged. Driven by the janitor.
func (s *Service) PurgeExpired(ctx context.Context) (int, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT DISTINCT trash_batch, tenant_id FROM (
			SELECT trash_batch, tenant_id FROM files
			 WHERE status = 'trashed' AND trash_batch IS NOT NULL AND purge_at < NOW()
			UNION
			SELECT trash_batch, tenant_id FROM folders
			 WHERE status = 'trashed' AND trash_batch IS NOT NULL AND purge_at < NOW()
		) t`)
	if err != nil {
		return 0, fmt.Errorf("trash: scan expired: %w", err)
	}
	type ref struct{ batch, tenant int64 }
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.batch, &r.tenant); err != nil {
			rows.Close()
			return 0, fmt.Errorf("trash: scan expired row: %w", err)
		}
		refs = append(refs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("trash: expired rows: %w", err)
	}

	purged := 0
	for _, r := range refs {
		if err := s.PurgeBatch(ctx, r.tenant, r.batch); err != nil {
			logging.Error(packageName, "PurgeExpired: batch=%d tenant=%d failed: %v", r.batch, r.tenant, err)
			continue
		}
		purged++
	}
	if purged > 0 {
		logging.Info(packageName, "PurgeExpired: purged %d expired batch(es)", purged)
	}
	return purged, nil
}

// ----- internals -----------------------------------------------------------

// pathOf resolves a folder id to a breadcrumb path ("/A/B"), "/" for the
// tenant root (nil). Best-effort.
func (s *Service) pathOf(ctx context.Context, tenantID int64, folderID *int64) string {
	if folderID == nil {
		return "/"
	}
	rows, err := s.client.QueryContext(ctx, `
		WITH RECURSIVE walk AS (
			SELECT id, parent_folder_id, name, 0 AS depth
			  FROM folders WHERE id = $1 AND tenant_id = $2
			UNION ALL
			SELECT p.id, p.parent_folder_id, p.name, walk.depth + 1
			  FROM folders p JOIN walk ON walk.parent_folder_id = p.id
			 WHERE p.tenant_id = $2
		)
		SELECT name FROM walk ORDER BY depth DESC`, *folderID, tenantID)
	if err != nil {
		return "/"
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "/"
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return "/"
	}
	return "/" + strings.Join(names, "/")
}

// resolveRestoreName returns the root's name, or a " (restored)" variant if
// an active sibling already owns that name at the destination.
func (s *Service) resolveRestoreName(ctx context.Context, tenantID int64, root *RootRef, target *int64) (string, error) {
	table := "files"
	if root.Kind == KindFolder {
		table = "folders"
	}
	free, err := s.nameFree(ctx, tenantID, table, target, root.Name, root.ID)
	if err != nil {
		return "", err
	}
	if free {
		return root.Name, nil
	}
	base := root.Name
	for i := 1; i <= 99; i++ {
		cand := fmt.Sprintf("%s (restored%s)", base, suffixNum(i))
		free, err := s.nameFree(ctx, tenantID, table, target, cand, root.ID)
		if err != nil {
			return "", err
		}
		if free {
			return cand, nil
		}
	}
	return root.Name, nil // give up gracefully; let the unique index decide
}

func suffixNum(i int) string {
	if i == 1 {
		return ""
	}
	return fmt.Sprintf(" %d", i)
}

func (s *Service) nameFree(ctx context.Context, tenantID int64, table string, parent *int64, name string, excludeID int64) (bool, error) {
	var exists bool
	var err error
	if parent == nil {
		err = s.client.QueryRowContext(ctx, fmt.Sprintf(`
			SELECT EXISTS(SELECT 1 FROM %s
			               WHERE tenant_id = $1 AND parent_folder_id IS NULL
			                 AND status = 'active' AND lower(name) = lower($2) AND id <> $3)`, table),
			tenantID, name, excludeID).Scan(&exists)
	} else {
		err = s.client.QueryRowContext(ctx, fmt.Sprintf(`
			SELECT EXISTS(SELECT 1 FROM %s
			               WHERE tenant_id = $1 AND parent_folder_id = $2
			                 AND status = 'active' AND lower(name) = lower($3) AND id <> $4)`, table),
			tenantID, *parent, name, excludeID).Scan(&exists)
	}
	if err != nil {
		return false, fmt.Errorf("trash: name check: %w", err)
	}
	return !exists, nil
}

// scopeClause returns an AND fragment binding $idx plus its arg, or ("", nil)
// when scope is nil. ::bigint[] lets an empty set match nothing.
func scopeClause(scope *Scope, col string, idx int) (string, []any) {
	if scope == nil {
		return "", nil
	}
	// In scope: items whose original parent is a granted folder, OR root-level
	// items (no parent) in a workspace the member is assigned to wholesale.
	return fmt.Sprintf(" AND (%s = ANY($%d::bigint[]) OR (%s IS NULL AND f.workspace_id = ANY($%d::bigint[])))",
			col, idx, col, idx+1),
		[]any{int64Slice(scope.IDs), int64Slice(scope.WorkspaceIDs)}
}
