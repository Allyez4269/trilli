package folders

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"trilli/system/database/postgres"
	"trilli/system/logging"
)

// copySuffixRe matches a trailing "_copy_NN" so renames of an already
// renamed folder pick a new index instead of nesting suffixes.
var copySuffixRe = regexp.MustCompile(`_copy_\d+$`)

// Service is the folder business logic — all methods tenant-scoped.
type Service struct {
	client *postgres.Client
}

// NewService returns a wired folders Service.
func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

// ----- Create --------------------------------------------------------------

// CreateInput is what Create needs.
type CreateInput struct {
	TenantID        int64
	CreatedByUserID int64
	Name            string
	ParentFolderID  *int64 // nil = create at workspace root
	WorkspaceID     *int64 // optional; nil = inherit parent's workspace, else account default
}

// Create inserts a new folder. Returns ErrNameTaken if a sibling with the same
// (case-insensitive) name already exists.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Folder, error) {
	name, err := normalizeName(in.Name)
	if err != nil {
		return nil, err
	}

	if in.ParentFolderID != nil {
		if err := s.assertParentActive(ctx, in.TenantID, *in.ParentFolderID); err != nil {
			return nil, err
		}
	}

	// A subfolder inherits its parent's workspace; a root folder uses the
	// explicit workspace or the account default.
	workspaceID, err := s.resolveWorkspaceID(ctx, in.TenantID, in.ParentFolderID, in.WorkspaceID)
	if err != nil {
		return nil, err
	}

	var f Folder
	err = s.client.QueryRowContext(ctx, `
		INSERT INTO folders (tenant_id, parent_folder_id, name, created_by_user_id, workspace_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, tenant_id, parent_folder_id, name, created_by_user_id,
		          status, created_at, updated_at, deleted_at`,
		in.TenantID, in.ParentFolderID, name, in.CreatedByUserID, workspaceID,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.CreatedByUserID,
		&f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return nil, ErrNameTaken
		}
		return nil, fmt.Errorf("folders: insert: %w", err)
	}
	logging.Info(packageName, "Create: tenant=%d folder=%d name=%q parent=%v workspace=%d", in.TenantID, f.ID, name, in.ParentFolderID, workspaceID)
	return &f, nil
}

// CreateCopy creates a folder, keeping the desired name when it's free in the
// parent, otherwise walking "<name>_copy_NN" (the same collision pattern as a
// move) until one is free. Used by same-account folder copy/duplicate so a paste
// into a folder without a name clash keeps the original name, while a paste into
// the source folder (or any clash) auto-renames.
func (s *Service) CreateCopy(ctx context.Context, in CreateInput) (*Folder, error) {
	desired := strings.TrimSpace(in.Name)
	stripped := strings.TrimRight(copySuffixRe.ReplaceAllString(desired, ""), "_ \t")
	for i := 0; i <= 99; i++ {
		candidate := desired
		if i >= 1 {
			candidate = fmt.Sprintf("%s_copy_%02d", stripped, i)
		}
		attempt := in
		attempt.Name = candidate
		f, err := s.Create(ctx, attempt)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, ErrNameTaken) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("folders: too many copies of %q under this parent", desired)
}

// resolveWorkspaceID picks the workspace a new folder belongs to: a subfolder
// inherits its parent's workspace; a root folder uses the explicit workspace
// (validated against the tenant) or the account's default (oldest) workspace.
func (s *Service) resolveWorkspaceID(ctx context.Context, tenantID int64, parentFolderID, explicit *int64) (int64, error) {
	if parentFolderID != nil {
		var wsID sql.NullInt64
		err := s.client.QueryRowContext(ctx,
			`SELECT workspace_id FROM folders WHERE id = $1 AND tenant_id = $2`,
			*parentFolderID, tenantID,
		).Scan(&wsID)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrInvalidParent
		}
		if err != nil {
			return 0, fmt.Errorf("folders: resolve parent workspace: %w", err)
		}
		if wsID.Valid {
			return wsID.Int64, nil
		}
		// Parent created in the migration gap with no workspace yet — fall back.
	}
	if explicit != nil {
		var ok bool
		if err := s.client.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
			*explicit, tenantID,
		).Scan(&ok); err != nil {
			return 0, fmt.Errorf("folders: validate workspace: %w", err)
		}
		if !ok {
			return 0, ErrInvalidWorkspace
		}
		return *explicit, nil
	}
	var wsID int64
	err := s.client.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE tenant_id = $1 AND status = 'active' ORDER BY created_at, id LIMIT 1`,
		tenantID,
	).Scan(&wsID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("folders: account %d has no active workspace", tenantID)
	}
	if err != nil {
		return 0, fmt.Errorf("folders: default workspace: %w", err)
	}
	return wsID, nil
}

// ----- Read ----------------------------------------------------------------

// Get loads a single active folder by id, tenant-scoped.
func (s *Service) Get(ctx context.Context, tenantID, folderID int64) (*Folder, error) {
	var f Folder
	err := s.client.QueryRowContext(ctx, `
		SELECT id, tenant_id, parent_folder_id, name, created_by_user_id,
		       status, created_at, updated_at, deleted_at
		  FROM folders WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
		folderID, tenantID,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.CreatedByUserID,
		&f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFolderNotFound
		}
		return nil, fmt.Errorf("folders: get: %w", err)
	}
	return &f, nil
}

// ListChildren returns folders directly under parentFolderID (nil = root),
// sorted case-insensitively by name. Each row's SizeBytes is the recursive
// sum of active file sizes anywhere in that folder's subtree, computed in
// a single recursive CTE so we don't N+1 the database when listing.
func (s *Service) ListChildren(ctx context.Context, tenantID int64, parentFolderID *int64) ([]*Folder, error) {
	return s.ListChildrenScoped(ctx, tenantID, parentFolderID, nil)
}

// ListChildrenScoped is ListChildren with an optional workspace filter on the
// immediate children — used to list a specific workspace's root folders (when
// parentFolderID is nil and workspaceID is set).
func (s *Service) ListChildrenScoped(ctx context.Context, tenantID int64, parentFolderID, workspaceID *int64) ([]*Folder, error) {
	const query = `
		WITH RECURSIVE
		children AS (
			SELECT id, tenant_id, parent_folder_id, name, created_by_user_id,
			       status, created_at, updated_at, deleted_at, starred_at
			  FROM folders
			 WHERE tenant_id = $1 AND status = 'active'
			   AND parent_folder_id IS NOT DISTINCT FROM $2::bigint
			   AND ($3::bigint IS NULL OR workspace_id = $3)
		),
		walk AS (
			SELECT id AS root_id, id AS current_id FROM children
			UNION ALL
			SELECT w.root_id, f.id
			  FROM walk w
			  JOIN folders f ON f.parent_folder_id = w.current_id
			 WHERE f.tenant_id = $1 AND f.status = 'active'
		),
		sums AS (
			SELECT w.root_id AS folder_id,
			       COALESCE(SUM(fl.size_bytes), 0) AS bytes
			  FROM walk w
			  LEFT JOIN files fl
			    ON fl.parent_folder_id = w.current_id
			   AND fl.tenant_id = $1
			   AND fl.status = 'active'
			 GROUP BY w.root_id
		),
		direct_file_counts AS (
			SELECT fl.parent_folder_id AS folder_id, COUNT(*) AS n
			  FROM files fl
			 WHERE fl.tenant_id = $1 AND fl.status = 'active'
			   AND fl.parent_folder_id IN (SELECT id FROM children)
			 GROUP BY fl.parent_folder_id
		),
		direct_folder_counts AS (
			SELECT fo.parent_folder_id AS folder_id, COUNT(*) AS n
			  FROM folders fo
			 WHERE fo.tenant_id = $1 AND fo.status = 'active'
			   AND fo.parent_folder_id IN (SELECT id FROM children)
			 GROUP BY fo.parent_folder_id
		)
		SELECT c.id, c.tenant_id, c.parent_folder_id, c.name, c.created_by_user_id,
		       c.status, c.created_at, c.updated_at, c.deleted_at, c.starred_at,
		       COALESCE(s.bytes, 0),
		       COALESCE(dfc.n, 0) + COALESCE(dfoc.n, 0) AS item_count,
		       COALESCE(dfc.n, 0) AS file_count
		  FROM children c
		  LEFT JOIN sums s ON s.folder_id = c.id
		  LEFT JOIN direct_file_counts dfc ON dfc.folder_id = c.id
		  LEFT JOIN direct_folder_counts dfoc ON dfoc.folder_id = c.id
		 ORDER BY lower(c.name)`

	rows, err := s.client.QueryContext(ctx, query, tenantID, parentFolderID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("folders: list children: %w", err)
	}
	defer rows.Close()

	out := make([]*Folder, 0)
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.CreatedByUserID,
			&f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt, &f.SizeBytes, &f.ItemCount, &f.FileCount); err != nil {
			return nil, fmt.Errorf("folders: scan: %w", err)
		}
		out = append(out, &f)
	}
	return out, rows.Err()
}

// ListByIDs returns the given folders (tenant-scoped, active only) enriched
// with the same recursive size + item-count totals as ListChildren, ordered
// by name. Used to surface a scoped member's granted folders as entry points
// at the tenant root. Ids outside the tenant or inactive are simply omitted.
func (s *Service) ListByIDs(ctx context.Context, tenantID int64, ids []int64) ([]*Folder, error) {
	if len(ids) == 0 {
		return []*Folder{}, nil
	}
	const query = `
		WITH RECURSIVE
		children AS (
			SELECT id, tenant_id, parent_folder_id, name, created_by_user_id,
			       status, created_at, updated_at, deleted_at, starred_at
			  FROM folders
			 WHERE tenant_id = $1 AND status = 'active'
			   AND id = ANY($2)
		),
		walk AS (
			SELECT id AS root_id, id AS current_id FROM children
			UNION ALL
			SELECT w.root_id, f.id
			  FROM walk w
			  JOIN folders f ON f.parent_folder_id = w.current_id
			 WHERE f.tenant_id = $1 AND f.status = 'active'
		),
		sums AS (
			SELECT w.root_id AS folder_id,
			       COALESCE(SUM(fl.size_bytes), 0) AS bytes
			  FROM walk w
			  LEFT JOIN files fl
			    ON fl.parent_folder_id = w.current_id
			   AND fl.tenant_id = $1
			   AND fl.status = 'active'
			 GROUP BY w.root_id
		),
		direct_file_counts AS (
			SELECT fl.parent_folder_id AS folder_id, COUNT(*) AS n
			  FROM files fl
			 WHERE fl.tenant_id = $1 AND fl.status = 'active'
			   AND fl.parent_folder_id IN (SELECT id FROM children)
			 GROUP BY fl.parent_folder_id
		),
		direct_folder_counts AS (
			SELECT fo.parent_folder_id AS folder_id, COUNT(*) AS n
			  FROM folders fo
			 WHERE fo.tenant_id = $1 AND fo.status = 'active'
			   AND fo.parent_folder_id IN (SELECT id FROM children)
			 GROUP BY fo.parent_folder_id
		)
		SELECT c.id, c.tenant_id, c.parent_folder_id, c.name, c.created_by_user_id,
		       c.status, c.created_at, c.updated_at, c.deleted_at, c.starred_at,
		       COALESCE(s.bytes, 0),
		       COALESCE(dfc.n, 0) + COALESCE(dfoc.n, 0) AS item_count,
		       COALESCE(dfc.n, 0) AS file_count
		  FROM children c
		  LEFT JOIN sums s ON s.folder_id = c.id
		  LEFT JOIN direct_file_counts dfc ON dfc.folder_id = c.id
		  LEFT JOIN direct_folder_counts dfoc ON dfoc.folder_id = c.id
		 ORDER BY lower(c.name)`

	rows, err := s.client.QueryContext(ctx, query, tenantID, int64Slice(ids))
	if err != nil {
		return nil, fmt.Errorf("folders: list by ids: %w", err)
	}
	defer rows.Close()

	out := make([]*Folder, 0, len(ids))
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.CreatedByUserID,
			&f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt, &f.SizeBytes, &f.ItemCount, &f.FileCount); err != nil {
			return nil, fmt.Errorf("folders: scan by id: %w", err)
		}
		out = append(out, &f)
	}
	return out, rows.Err()
}

// Breadcrumb walks from folder up to root and returns items in root→leaf order.
// Used by the UI to render "Workspace / Documents / Reports".
func (s *Service) Breadcrumb(ctx context.Context, tenantID, folderID int64) ([]BreadcrumbItem, error) {
	rows, err := s.client.QueryContext(ctx, `
		WITH RECURSIVE walk AS (
			SELECT id, parent_folder_id, name, COALESCE(protected_source, '') AS ps, 0 AS depth
			  FROM folders
			 WHERE id = $1 AND tenant_id = $2 AND status = 'active'
			UNION ALL
			SELECT p.id, p.parent_folder_id, p.name, COALESCE(p.protected_source, ''), walk.depth + 1
			  FROM folders p JOIN walk ON walk.parent_folder_id = p.id
			 WHERE p.tenant_id = $2 AND p.status = 'active'
		)
		SELECT id, name, ps FROM walk ORDER BY depth DESC`, folderID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("folders: breadcrumb: %w", err)
	}
	defer rows.Close()

	items := make([]BreadcrumbItem, 0, 4)
	for rows.Next() {
		var it BreadcrumbItem
		if err := rows.Scan(&it.ID, &it.Name, &it.ProtectedSource); err != nil {
			return nil, fmt.Errorf("folders: scan breadcrumb: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// ----- Mutate --------------------------------------------------------------

// Rename changes a folder's name in its current location.
func (s *Service) Rename(ctx context.Context, tenantID, folderID int64, newName string) (*Folder, error) {
	name, err := normalizeName(newName)
	if err != nil {
		return nil, err
	}
	var f Folder
	err = s.client.QueryRowContext(ctx, `
		UPDATE folders SET name = $1, updated_at = NOW()
		 WHERE id = $2 AND tenant_id = $3 AND status = 'active'
		RETURNING id, tenant_id, parent_folder_id, name, created_by_user_id,
		          status, created_at, updated_at, deleted_at`,
		name, folderID, tenantID,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.CreatedByUserID,
		&f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFolderNotFound
		}
		if pgUniqueViolation(err) {
			return nil, ErrNameTaken
		}
		return nil, fmt.Errorf("folders: rename: %w", err)
	}
	return &f, nil
}

// ListStarred returns the account's starred folders (most-recently-starred
// first), enriched with workspace + a direct item count for the Starred page.
// restrict scopes the result to a folder-id set (scoped members); a nil slice
// means unrestricted (owner/admin), an empty slice means "nothing accessible".
func (s *Service) ListStarred(ctx context.Context, tenantID int64, restrict []int64) ([]*Folder, error) {
	query := `
		SELECT f.id, f.tenant_id, f.parent_folder_id, f.name, f.created_by_user_id,
		       f.status, f.created_at, f.updated_at, f.deleted_at, f.starred_at, f.workspace_id,
		       COALESCE((SELECT SUM(size_bytes) FROM files
		                  WHERE parent_folder_id = f.id AND tenant_id = $1 AND status = 'active'), 0),
		       (SELECT COUNT(*) FROM folders c WHERE c.parent_folder_id = f.id AND c.tenant_id = $1 AND c.status = 'active')
		         + (SELECT COUNT(*) FROM files fl WHERE fl.parent_folder_id = f.id AND fl.tenant_id = $1 AND fl.status = 'active'),
		       (SELECT COUNT(*) FROM files fl WHERE fl.parent_folder_id = f.id AND fl.tenant_id = $1 AND fl.status = 'active') AS file_count
		  FROM folders f
		 WHERE f.tenant_id = $1 AND f.status = 'active' AND f.starred_at IS NOT NULL`
	args := []any{tenantID}
	if restrict != nil {
		query += ` AND f.id = ANY($2)`
		args = append(args, int64Slice(restrict))
	}
	query += ` ORDER BY f.starred_at DESC, lower(f.name)`

	rows, err := s.client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("folders: list starred: %w", err)
	}
	defer rows.Close()

	out := make([]*Folder, 0)
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.CreatedByUserID,
			&f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt, &f.WorkspaceID,
			&f.SizeBytes, &f.ItemCount, &f.FileCount); err != nil {
			return nil, fmt.Errorf("folders: scan starred: %w", err)
		}
		out = append(out, &f)
	}
	return out, rows.Err()
}

// SetStarred stars/unstars a folder (mirrors files.SetStarred). Idempotent —
// re-starring preserves the original starred_at.
func (s *Service) SetStarred(ctx context.Context, tenantID, folderID int64, starred bool) (*Folder, error) {
	setClause := "starred_at = NULL"
	if starred {
		setClause = "starred_at = COALESCE(starred_at, NOW())"
	}
	var f Folder
	err := s.client.QueryRowContext(ctx, `
		UPDATE folders SET `+setClause+`, updated_at = NOW()
		 WHERE id = $1 AND tenant_id = $2 AND status = 'active'
		RETURNING id, tenant_id, parent_folder_id, name, created_by_user_id,
		          status, created_at, updated_at, deleted_at, starred_at`,
		folderID, tenantID,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.CreatedByUserID,
		&f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt, &f.StarredAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFolderNotFound
		}
		return nil, fmt.Errorf("folders: set starred: %w", err)
	}
	return &f, nil
}

// Move re-parents a folder. newParentID nil = move to root. Prevents cycles.
// If a sibling folder with the same (case-insensitive) name already lives in
// the destination, the moved folder is auto-renamed "<name>_copy_NN" so the
// move always succeeds instead of erroring with ErrNameTaken.
// Move relocates a folder. When the destination is in a DIFFERENT workspace,
// the whole subtree (this folder, its descendant folders, and all their files)
// is reassigned to the target workspace and both workspaces' storage counters
// are reconciled — enabling cross-workspace transfers of folders. The target
// workspace is the destination parent's own, or targetWorkspaceID for root
// moves (newParentID == nil).
func (s *Service) Move(ctx context.Context, tenantID, folderID int64, newParentID *int64, targetWorkspaceID *int64) (*Folder, error) {
	if newParentID != nil && *newParentID == folderID {
		return nil, ErrCycle
	}
	if newParentID != nil {
		if err := s.assertParentActive(ctx, tenantID, *newParentID); err != nil {
			return nil, err
		}
		if err := s.assertNoCycle(ctx, tenantID, folderID, *newParentID); err != nil {
			return nil, err
		}
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("folders: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var currentName string
	var currentWS int64
	err = tx.QueryRowContext(ctx, `
		SELECT name, workspace_id FROM folders
		 WHERE id = $1 AND tenant_id = $2 AND status = 'active'
		 FOR UPDATE`, folderID, tenantID).Scan(&currentName, &currentWS)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFolderNotFound
		}
		return nil, fmt.Errorf("folders: load for move: %w", err)
	}

	// Resolve the destination workspace.
	targetWS := currentWS
	if newParentID != nil {
		if err := tx.QueryRowContext(ctx,
			`SELECT workspace_id FROM folders WHERE id = $1 AND tenant_id = $2`,
			*newParentID, tenantID).Scan(&targetWS); err != nil {
			return nil, fmt.Errorf("folders: resolve destination workspace: %w", err)
		}
	} else if targetWorkspaceID != nil {
		var ok bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
			*targetWorkspaceID, tenantID).Scan(&ok); err != nil {
			return nil, fmt.Errorf("folders: check target workspace: %w", err)
		}
		if !ok {
			return nil, ErrFolderNotFound
		}
		targetWS = *targetWorkspaceID
	}

	finalName, err := resolveAvailableFolderName(ctx, tx, tenantID, newParentID, currentName, folderID)
	if err != nil {
		return nil, err
	}

	var f Folder
	err = tx.QueryRowContext(ctx, `
		UPDATE folders SET parent_folder_id = $1, name = $2, workspace_id = $3, updated_at = NOW()
		 WHERE id = $4 AND tenant_id = $5 AND status = 'active'
		RETURNING id, tenant_id, parent_folder_id, name, created_by_user_id,
		          status, created_at, updated_at, deleted_at`,
		newParentID, finalName, targetWS, folderID, tenantID,
	).Scan(&f.ID, &f.TenantID, &f.ParentFolderID, &f.Name, &f.CreatedByUserID,
		&f.Status, &f.CreatedAt, &f.UpdatedAt, &f.DeletedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return nil, ErrNameTaken
		}
		return nil, fmt.Errorf("folders: move: %w", err)
	}

	// Cross-workspace transfer: cascade the new workspace onto the whole subtree
	// (descendant folders + their files), then reconcile both workspaces.
	if targetWS != currentWS {
		subtreeCTE := `
			WITH RECURSIVE sub AS (
				SELECT id FROM folders WHERE id = $1 AND tenant_id = $2
				UNION ALL
				SELECT c.id FROM folders c JOIN sub ON c.parent_folder_id = sub.id
				 WHERE c.tenant_id = $2
			)`
		if _, err := tx.ExecContext(ctx, subtreeCTE+`
			UPDATE folders SET workspace_id = $3, updated_at = NOW()
			 WHERE tenant_id = $2 AND id IN (SELECT id FROM sub)`,
			folderID, tenantID, targetWS); err != nil {
			return nil, fmt.Errorf("folders: cascade subtree workspace: %w", err)
		}
		if _, err := tx.ExecContext(ctx, subtreeCTE+`
			UPDATE files SET workspace_id = $3, updated_at = NOW()
			 WHERE tenant_id = $2 AND parent_folder_id IN (SELECT id FROM sub)`,
			folderID, tenantID, targetWS); err != nil {
			return nil, fmt.Errorf("folders: cascade subtree files workspace: %w", err)
		}
		if err := reconcileWorkspaceStorage(ctx, tx, tenantID, currentWS); err != nil {
			return nil, err
		}
		if err := reconcileWorkspaceStorage(ctx, tx, tenantID, targetWS); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("folders: commit move: %w", err)
	}
	committed = true
	return &f, nil
}

// reconcileWorkspaceStorage recomputes a workspace's storage_bytes_used as the
// true SUM of its files' sizes (idempotent — keeps per-workspace meters honest
// after a cross-workspace move).
func reconcileWorkspaceStorage(ctx context.Context, tx *sql.Tx, tenantID, workspaceID int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE workspaces
		   SET storage_bytes_used = COALESCE(
		         (SELECT SUM(size_bytes) FROM files WHERE tenant_id = $1 AND workspace_id = $2), 0),
		       updated_at = NOW()
		 WHERE id = $2 AND tenant_id = $1`, tenantID, workspaceID)
	if err != nil {
		return fmt.Errorf("folders: reconcile workspace storage: %w", err)
	}
	return nil
}

// resolveAvailableFolderName returns desired if no active sibling owns that
// name in (tenantID, parentID); otherwise walks "<name>_copy_01", _copy_02,
// … up to 99. excludeFolderID lets us skip the row being moved.
func resolveAvailableFolderName(ctx context.Context, tx *sql.Tx, tenantID int64, parentID *int64, desired string, excludeFolderID int64) (string, error) {
	free, err := folderNameIsFree(ctx, tx, tenantID, parentID, desired, excludeFolderID)
	if err != nil {
		return "", err
	}
	if free {
		return desired, nil
	}
	stripped := copySuffixRe.ReplaceAllString(desired, "")
	stripped = strings.TrimRight(stripped, "_ \t")
	for i := 1; i <= 99; i++ {
		candidate := fmt.Sprintf("%s_copy_%02d", stripped, i)
		free, err := folderNameIsFree(ctx, tx, tenantID, parentID, candidate, excludeFolderID)
		if err != nil {
			return "", err
		}
		if free {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("folders: too many copies of %q under this parent", desired)
}

func folderNameIsFree(ctx context.Context, tx *sql.Tx, tenantID int64, parentID *int64, name string, excludeFolderID int64) (bool, error) {
	// Normalize the comparison so trailing underscores/whitespace don't
	// disguise duplicates ("Reports_" matches "Reports").
	normTarget := strings.ToLower(strings.TrimRight(name, "_ \t"))
	const stripPattern = `[_[:space:]]+$`
	var exists bool
	var err error
	if parentID == nil {
		err = tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM folders
			               WHERE tenant_id = $1 AND parent_folder_id IS NULL
			                 AND status = 'active'
			                 AND regexp_replace(lower(name), $4, '') = $2
			                 AND ($3 = 0 OR id <> $3))`,
			tenantID, normTarget, excludeFolderID, stripPattern).Scan(&exists)
	} else {
		err = tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM folders
			               WHERE tenant_id = $1 AND parent_folder_id = $2
			                 AND status = 'active'
			                 AND regexp_replace(lower(name), $5, '') = $3
			                 AND ($4 = 0 OR id <> $4))`,
			tenantID, *parentID, normTarget, excludeFolderID, stripPattern).Scan(&exists)
	}
	if err != nil {
		return false, fmt.Errorf("folders: name check: %w", err)
	}
	return !exists, nil
}

// Trash soft-deletes a folder AND its entire active subtree (descendant
// folders + files) as one batch, so the trash UI shows a single row for the
// deletion and Restore brings the whole subtree back together. The folder the
// caller deleted is flagged trash_root; descendants are not. Bytes are kept
// and quota is NOT decremented — trashed items still count until purged.
func (s *Service) Trash(ctx context.Context, tenantID, folderID, deletedByUserID int64) error {
	// Collect the active subtree folder ids (including the root) up front so
	// both the folder + file updates target the same set.
	subtree, err := s.activeSubtreeIDs(ctx, tenantID, folderID)
	if err != nil {
		return err
	}
	if len(subtree) == 0 {
		return ErrFolderNotFound // folder missing or already trashed
	}

	// A folder containing PROTECTED files (Trilli Sign deposits) can't be
	// trashed — those files are removable only via their envelope, and
	// sweeping them into trash would sever that link.
	var protected int
	if err := s.client.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM files
		         WHERE tenant_id = $1 AND status = 'active' AND COALESCE(protected_source, '') <> ''
		           AND parent_folder_id = ANY($2))
		     + (SELECT count(*) FROM folders
		         WHERE tenant_id = $1 AND status = 'active' AND COALESCE(protected_source, '') <> ''
		           AND id = ANY($2))`,
		tenantID, int64Slice(subtree)).Scan(&protected); err == nil && protected > 0 {
		return ErrContainsProtected
	}

	var batch int64
	if err := s.client.QueryRowContext(ctx, `SELECT nextval('trash_batch_seq')`).Scan(&batch); err != nil {
		return fmt.Errorf("folders: alloc trash batch: %w", err)
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("folders: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Folders in the subtree — only the deleted folder is the batch root.
	if _, err := tx.ExecContext(ctx, `
		UPDATE folders
		   SET status = 'trashed', deleted_at = NOW(), updated_at = NOW(),
		       deleted_by_user_id = $3,
		       purge_at = NOW() + INTERVAL '7 days',
		       trash_batch = $4,
		       trash_root = (id = $1)
		 WHERE id = ANY($2) AND tenant_id = $5 AND status = 'active'`,
		folderID, int64Slice(subtree), deletedByUserID, batch, tenantID); err != nil {
		return fmt.Errorf("folders: trash subtree folders: %w", err)
	}

	// Files anywhere in the subtree — never roots (the folder is the root).
	if _, err := tx.ExecContext(ctx, `
		UPDATE files
		   SET status = 'trashed', deleted_at = NOW(), updated_at = NOW(),
		       deleted_by_user_id = $2,
		       purge_at = NOW() + INTERVAL '7 days',
		       trash_batch = $3,
		       trash_root = false
		 WHERE parent_folder_id = ANY($1) AND tenant_id = $4 AND status = 'active'`,
		int64Slice(subtree), deletedByUserID, batch, tenantID); err != nil {
		return fmt.Errorf("folders: trash subtree files: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("folders: commit trash: %w", err)
	}
	committed = true
	logging.Info(packageName, "Trash: tenant=%d folder=%d batch=%d subtree=%d by=%d",
		tenantID, folderID, batch, len(subtree), deletedByUserID)
	return nil
}

// activeSubtreeIDs returns the ids of the given folder plus all its active
// descendant folders. Empty if the folder is missing or not active.
func (s *Service) activeSubtreeIDs(ctx context.Context, tenantID, folderID int64) ([]int64, error) {
	rows, err := s.client.QueryContext(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id FROM folders
			 WHERE id = $1 AND tenant_id = $2 AND status = 'active'
			UNION ALL
			SELECT f.id FROM folders f
			  JOIN subtree s ON f.parent_folder_id = s.id
			 WHERE f.tenant_id = $2 AND f.status = 'active'
		)
		SELECT id FROM subtree`, folderID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("folders: subtree: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("folders: scan subtree: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ----- internals -----------------------------------------------------------

func (s *Service) assertParentActive(ctx context.Context, tenantID, parentID int64) error {
	var exists bool
	if err := s.client.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM folders
		               WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
		parentID, tenantID).Scan(&exists); err != nil {
		return fmt.Errorf("folders: parent check: %w", err)
	}
	if !exists {
		return ErrInvalidParent
	}
	return nil
}

// assertNoCycle walks candidateParent up the ancestor chain. If it ever reaches
// folderID, the candidate is a descendant of folderID and moving folderID into
// it would create a cycle.
func (s *Service) assertNoCycle(ctx context.Context, tenantID, folderID, candidateParent int64) error {
	current := candidateParent
	for depth := 0; depth < 100; depth++ {
		if current == folderID {
			return ErrCycle
		}
		var parentID *int64
		err := s.client.QueryRowContext(ctx, `
			SELECT parent_folder_id FROM folders
			 WHERE id = $1 AND tenant_id = $2`, current, tenantID).Scan(&parentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrInvalidParent
			}
			return fmt.Errorf("folders: cycle walk: %w", err)
		}
		if parentID == nil {
			return nil
		}
		current = *parentID
	}
	return fmt.Errorf("folders: cycle check exceeded max depth")
}

// AssertNotSelfOrDescendant returns ErrCycle when destParent is the folder
// itself or any folder within its subtree. Used to stop a copy/paste of a folder
// into itself, which would recurse forever and runaway-duplicate data. A nil
// destParent (workspace root) is always safe.
func (s *Service) AssertNotSelfOrDescendant(ctx context.Context, tenantID, folderID int64, destParent *int64) error {
	if destParent == nil {
		return nil
	}
	return s.assertNoCycle(ctx, tenantID, folderID, *destParent)
}

func normalizeName(raw string) (string, error) {
	n := strings.TrimSpace(raw)
	if n == "" || len(n) > 255 || strings.ContainsAny(n, "/\\") {
		return "", ErrInvalidName
	}
	return n, nil
}

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// int64Slice renders []int64 as a Postgres array literal for ANY($N). The
// pgx stdlib driver accepts a plain []int64, but database/sql wants an
// explicit driver.Valuer; this keeps call sites readable.
type int64Slice []int64

func (s int64Slice) Value() (driver.Value, error) {
	if len(s) == 0 {
		return "{}", nil
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, v := range s {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", v)
	}
	b.WriteByte('}')
	return b.String(), nil
}
