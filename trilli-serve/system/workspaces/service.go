package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"trilli/system/auth"
	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const maxNameLen = 120

// Service wires the DB client for workspace business logic.
type Service struct {
	client *postgres.Client
}

// NewService constructs a workspaces Service.
func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

// ListForIdentity returns the workspaces the caller may access: every active
// workspace in the account for owner/admin; the assigned set for member/viewer.
func (s *Service) ListForIdentity(ctx context.Context, identity *auth.Identity) ([]Workspace, error) {
	if identity == nil || identity.Tenant == nil || identity.Membership == nil {
		return nil, ErrForbidden
	}
	var (
		rows *sql.Rows
		err  error
	)
	if auth.IsAdminOrOwner(identity.Membership.Role) {
		rows, err = s.client.QueryContext(ctx, `
			SELECT id, tenant_id, name, disk_allocation_bytes, storage_bytes_used, status, created_at
			  FROM workspaces
			 WHERE tenant_id = $1 AND status = 'active'
			 ORDER BY lower(name)`, identity.Tenant.ID)
	} else {
		// A member/viewer reaches a workspace either by whole-workspace
		// assignment (workspace_members) OR by a folder grant inside it
		// (tenant_member_folders → folders.workspace_id).
		rows, err = s.client.QueryContext(ctx, `
			SELECT w.id, w.tenant_id, w.name, w.disk_allocation_bytes, w.storage_bytes_used, w.status, w.created_at
			  FROM workspaces w
			 WHERE w.tenant_id = $1 AND w.status = 'active'
			   AND (
			     EXISTS (SELECT 1 FROM workspace_members wm
			              WHERE wm.workspace_id = w.id AND wm.user_id = $2)
			     OR EXISTS (SELECT 1 FROM tenant_member_folders tmf
			                  JOIN folders f ON f.id = tmf.folder_id
			                  JOIN tenant_members tm ON tm.id = tmf.tenant_member_id
			                 WHERE f.workspace_id = w.id AND f.status = 'active'
			                   AND tm.user_id = $2 AND tm.tenant_id = $1)
			   )
			 ORDER BY lower(w.name)`, identity.Tenant.ID, identity.User.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("workspaces: list: %w", err)
	}
	defer rows.Close()

	out := make([]Workspace, 0)
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.TenantID, &w.Name, &w.DiskAllocationBytes,
			&w.StorageBytesUsed, &w.Status, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("workspaces: scan: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// AccountQuotaFor returns the account's plan total, the sum already allocated to
// active workspaces, and what remains to allocate.
func (s *Service) AccountQuotaFor(ctx context.Context, tenantID int64) (AccountQuota, error) {
	var q AccountQuota
	err := s.client.QueryRowContext(ctx, `
		SELECT COALESCE(t.quota_override_bytes, p.max_storage_bytes, 9223372036854775807),
		       COALESCE((SELECT sum(disk_allocation_bytes)
		                   FROM workspaces
		                  WHERE tenant_id = t.id AND status = 'active'), 0)
		  FROM tenants t
		  JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID,
	).Scan(&q.TotalBytes, &q.AllocatedBytes)
	if err != nil {
		return q, fmt.Errorf("workspaces: account quota: %w", err)
	}
	q.RemainingBytes = q.TotalBytes - q.AllocatedBytes
	if q.RemainingBytes < 0 {
		q.RemainingBytes = 0
	}
	return q, nil
}

// Create makes a new workspace, validating the name and that the requested
// allocation fits inside the account's remaining storage pool.
func (s *Service) Create(ctx context.Context, tenantID, createdBy int64, name string, allocBytes int64) (*Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return nil, ErrNameRequired
	}
	if allocBytes < 0 {
		return nil, ErrAllocInvalid
	}
	// Plan workspace cap. NULL max_workspaces = unlimited; otherwise refuse once
	// the account already has that many active workspaces.
	if err := s.checkWorkspaceLimit(ctx, tenantID); err != nil {
		return nil, err
	}
	q, err := s.AccountQuotaFor(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if allocBytes > q.RemainingBytes {
		return nil, ErrAllocExceedsPool
	}

	var w Workspace
	err = s.client.QueryRowContext(ctx, `
		INSERT INTO workspaces (tenant_id, name, disk_allocation_bytes, created_by_user_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, tenant_id, name, disk_allocation_bytes, storage_bytes_used, status, created_at`,
		tenantID, name, allocBytes, createdBy,
	).Scan(&w.ID, &w.TenantID, &w.Name, &w.DiskAllocationBytes, &w.StorageBytesUsed, &w.Status, &w.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return nil, ErrNameTaken
		}
		return nil, fmt.Errorf("workspaces: create: %w", err)
	}
	logging.Info(packageName, "Create: tenant=%d workspace=%d name=%q alloc=%d", tenantID, w.ID, name, allocBytes)
	return &w, nil
}

// checkWorkspaceLimit refuses creation when the account is at its plan's
// max_workspaces. NULL cap = unlimited (skip). Returns ErrWorkspaceLimit.
func (s *Service) checkWorkspaceLimit(ctx context.Context, tenantID int64) error {
	var maxWS sql.NullInt64
	if err := s.client.QueryRowContext(ctx, `
		SELECT p.max_workspaces
		  FROM tenants t JOIN plans p ON p.id = t.plan_id
		 WHERE t.id = $1`, tenantID).Scan(&maxWS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // no plan resolved — don't block
		}
		return fmt.Errorf("workspaces: load workspace cap: %w", err)
	}
	if !maxWS.Valid {
		return nil // unlimited
	}
	var count int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT count(*) FROM workspaces WHERE tenant_id = $1 AND status = 'active'`,
		tenantID).Scan(&count); err != nil {
		return fmt.Errorf("workspaces: count active: %w", err)
	}
	if count >= maxWS.Int64 {
		return ErrWorkspaceLimit
	}
	return nil
}

// UpdateInput carries the editable workspace fields (both optional).
type UpdateInput struct {
	Name       *string
	AllocBytes *int64
}

// Update changes a workspace's name and/or disk allocation. The new allocation
// can't drop below current usage, and the sum across the account's workspaces
// still can't exceed the plan total.
func (s *Service) Update(ctx context.Context, tenantID, workspaceID int64, in UpdateInput) (*Workspace, error) {
	cur, err := s.Get(ctx, tenantID, workspaceID)
	if err != nil {
		return nil, err
	}
	name := cur.Name
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > maxNameLen {
			return nil, ErrNameRequired
		}
		name = n
	}
	alloc := cur.DiskAllocationBytes
	if in.AllocBytes != nil {
		if *in.AllocBytes < 0 {
			return nil, ErrAllocInvalid
		}
		if *in.AllocBytes < cur.StorageBytesUsed {
			return nil, ErrAllocBelowUsage
		}
		q, err := s.AccountQuotaFor(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		// Pool check against everyone EXCEPT this workspace's current reservation.
		otherAllocated := q.AllocatedBytes - cur.DiskAllocationBytes
		if otherAllocated+*in.AllocBytes > q.TotalBytes {
			return nil, ErrAllocExceedsPool
		}
		alloc = *in.AllocBytes
	}

	var w Workspace
	err = s.client.QueryRowContext(ctx, `
		UPDATE workspaces SET name = $1, disk_allocation_bytes = $2, updated_at = NOW()
		 WHERE id = $3 AND tenant_id = $4 AND status = 'active'
		RETURNING id, tenant_id, name, disk_allocation_bytes, storage_bytes_used, status, created_at`,
		name, alloc, workspaceID, tenantID,
	).Scan(&w.ID, &w.TenantID, &w.Name, &w.DiskAllocationBytes, &w.StorageBytesUsed, &w.Status, &w.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return nil, ErrNameTaken
		}
		return nil, fmt.Errorf("workspaces: update: %w", err)
	}
	logging.Info(packageName, "Update: tenant=%d workspace=%d name=%q alloc=%d", tenantID, workspaceID, name, alloc)
	return &w, nil
}

// Get fetches an active workspace scoped to the tenant.
func (s *Service) Get(ctx context.Context, tenantID, workspaceID int64) (*Workspace, error) {
	var w Workspace
	err := s.client.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, disk_allocation_bytes, storage_bytes_used, status, created_at
		  FROM workspaces
		 WHERE id = $1 AND tenant_id = $2 AND status = 'active'`, workspaceID, tenantID,
	).Scan(&w.ID, &w.TenantID, &w.Name, &w.DiskAllocationBytes, &w.StorageBytesUsed, &w.Status, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("workspaces: get: %w", err)
	}
	return &w, nil
}

// Assign grants a tenant member whole-workspace access. Idempotent.
func (s *Service) Assign(ctx context.Context, tenantID, workspaceID, userID, assignedBy int64) error {
	if _, err := s.Get(ctx, tenantID, workspaceID); err != nil {
		return err
	}
	var isMember bool
	if err := s.client.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM tenant_members WHERE tenant_id = $1 AND user_id = $2)`,
		tenantID, userID,
	).Scan(&isMember); err != nil {
		return fmt.Errorf("workspaces: member check: %w", err)
	}
	if !isMember {
		return ErrNotTenantMember
	}
	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, assigned_by_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		workspaceID, userID, assignedBy,
	); err != nil {
		return fmt.Errorf("workspaces: assign: %w", err)
	}
	logging.Info(packageName, "Assign: tenant=%d workspace=%d user=%d", tenantID, workspaceID, userID)
	return nil
}

// Unassign removes a user's access to a workspace.
func (s *Service) Unassign(ctx context.Context, tenantID, workspaceID, userID int64) error {
	if _, err := s.Get(ctx, tenantID, workspaceID); err != nil {
		return err
	}
	if _, err := s.client.ExecContext(ctx,
		`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userID,
	); err != nil {
		return fmt.Errorf("workspaces: unassign: %w", err)
	}
	logging.Info(packageName, "Unassign: tenant=%d workspace=%d user=%d", tenantID, workspaceID, userID)
	return nil
}

// ListMembers returns the users assigned to a workspace.
func (s *Service) ListMembers(ctx context.Context, tenantID, workspaceID int64) ([]Member, error) {
	if _, err := s.Get(ctx, tenantID, workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.client.QueryContext(ctx, `
		SELECT u.id, u.email, u.full_name, wm.assigned_at
		  FROM workspace_members wm
		  JOIN users u ON u.id = wm.user_id
		 WHERE wm.workspace_id = $1
		 ORDER BY lower(u.email)`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspaces: list members: %w", err)
	}
	defer rows.Close()

	out := make([]Member, 0)
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.FullName, &m.AssignedAt); err != nil {
			return nil, fmt.Errorf("workspaces: scan member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListMemberWorkspaceIDs returns the ids of active workspaces the given user is
// assigned to within the tenant. Backs the GET that pre-fills the Workspace
// Access modal.
func (s *Service) ListMemberWorkspaceIDs(ctx context.Context, tenantID, userID int64) ([]int64, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT wm.workspace_id
		  FROM workspace_members wm
		  JOIN workspaces w ON w.id = wm.workspace_id
		 WHERE w.tenant_id = $1 AND wm.user_id = $2 AND w.status = 'active'
		 ORDER BY wm.workspace_id`, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("workspaces: list member workspace ids: %w", err)
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("workspaces: scan member workspace id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetMemberWorkspaces replaces a tenant member's workspace assignments with the
// given set. Validates the user is a member and that every workspace belongs to
// the active account, then diffs against the current set in one transaction.
func (s *Service) SetMemberWorkspaces(ctx context.Context, tenantID, userID int64, workspaceIDs []int64, assignedBy int64) error {
	var isMember bool
	if err := s.client.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM tenant_members WHERE tenant_id = $1 AND user_id = $2)`,
		tenantID, userID,
	).Scan(&isMember); err != nil {
		return fmt.Errorf("workspaces: member check: %w", err)
	}
	if !isMember {
		return ErrNotTenantMember
	}

	// Desired set, de-duplicated; validate each belongs to the active account.
	desired := make(map[int64]bool, len(workspaceIDs))
	for _, id := range workspaceIDs {
		if desired[id] {
			continue
		}
		var ok bool
		if err := s.client.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
			id, tenantID,
		).Scan(&ok); err != nil {
			return fmt.Errorf("workspaces: validate workspace %d: %w", id, err)
		}
		if !ok {
			return ErrNotFound
		}
		desired[id] = true
	}

	current, err := s.ListMemberWorkspaceIDs(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	currentSet := make(map[int64]bool, len(current))
	for _, id := range current {
		currentSet[id] = true
	}

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("workspaces: begin set member workspaces: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, id := range current {
		if !desired[id] {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
				id, userID,
			); err != nil {
				return fmt.Errorf("workspaces: remove assignment: %w", err)
			}
		}
	}
	for id := range desired {
		if !currentSet[id] {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO workspace_members (workspace_id, user_id, assigned_by_user_id)
				VALUES ($1, $2, $3)
				ON CONFLICT (workspace_id, user_id) DO NOTHING`,
				id, userID, assignedBy,
			); err != nil {
				return fmt.Errorf("workspaces: add assignment: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("workspaces: commit set member workspaces: %w", err)
	}
	committed = true
	logging.Info(packageName, "SetMemberWorkspaces: tenant=%d caller=%d user=%d workspaces=%v",
		tenantID, assignedBy, userID, workspaceIDs)
	return nil
}

// CanAccess reports whether the identity may use the given workspace: it must be
// active in the caller's account, and the caller must be an owner/admin or an
// assigned member.
func (s *Service) CanAccess(ctx context.Context, identity *auth.Identity, workspaceID int64) (bool, error) {
	if identity == nil || identity.Tenant == nil || identity.Membership == nil {
		return false, nil
	}
	var belongs bool
	if err := s.client.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1 AND tenant_id = $2 AND status = 'active')`,
		workspaceID, identity.Tenant.ID,
	).Scan(&belongs); err != nil {
		return false, fmt.Errorf("workspaces: access tenant check: %w", err)
	}
	if !belongs {
		return false, nil
	}
	if auth.IsAdminOrOwner(identity.Membership.Role) {
		return true, nil
	}
	// A member/viewer may open a workspace if assigned whole OR if they hold a
	// folder grant for any folder inside it.
	var assigned bool
	if err := s.client.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM workspace_members WHERE workspace_id = $1 AND user_id = $2)
		    OR EXISTS(SELECT 1 FROM tenant_member_folders tmf
		                JOIN folders f ON f.id = tmf.folder_id
		                JOIN tenant_members tm ON tm.id = tmf.tenant_member_id
		               WHERE f.workspace_id = $1 AND f.status = 'active'
		                 AND tm.user_id = $2 AND tm.tenant_id = $3)`,
		workspaceID, identity.User.ID, identity.Tenant.ID,
	).Scan(&assigned); err != nil {
		return false, fmt.Errorf("workspaces: access assigned check: %w", err)
	}
	return assigned, nil
}

// AccessRoster returns every member/viewer in the account with their current
// access to the given workspace — whole assignment and/or folder grants inside
// it. Backs the dual-pane access editor in the Edit Workspace modal.
func (s *Service) AccessRoster(ctx context.Context, tenantID, workspaceID int64) ([]MemberAccess, error) {
	if _, err := s.Get(ctx, tenantID, workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.client.QueryContext(ctx, `
		SELECT u.id, u.email, u.full_name, tm.role
		  FROM tenant_members tm
		  JOIN users u ON u.id = tm.user_id
		 WHERE tm.tenant_id = $1 AND tm.status = 'active'
		   AND lower(tm.role) IN ('member', 'viewer')
		 ORDER BY lower(COALESCE(u.full_name, u.email))`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("workspaces: access roster members: %w", err)
	}
	defer rows.Close()
	out := make([]MemberAccess, 0)
	idx := make(map[int64]int)
	for rows.Next() {
		var m MemberAccess
		if err := rows.Scan(&m.UserID, &m.Email, &m.FullName, &m.Role); err != nil {
			return nil, fmt.Errorf("workspaces: scan roster member: %w", err)
		}
		m.FolderIDs = []int64{}
		idx[m.UserID] = len(out)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Whole-workspace assignees.
	wholeRows, err := s.client.QueryContext(ctx,
		`SELECT user_id FROM workspace_members WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspaces: access roster whole: %w", err)
	}
	defer wholeRows.Close()
	for wholeRows.Next() {
		var uid int64
		if err := wholeRows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("workspaces: scan whole: %w", err)
		}
		if i, ok := idx[uid]; ok {
			out[i].Whole = true
		}
	}
	if err := wholeRows.Err(); err != nil {
		return nil, err
	}

	// Folder grants that live inside this workspace.
	fgRows, err := s.client.QueryContext(ctx, `
		SELECT tm.user_id, tmf.folder_id
		  FROM tenant_member_folders tmf
		  JOIN folders f ON f.id = tmf.folder_id
		  JOIN tenant_members tm ON tm.id = tmf.tenant_member_id
		 WHERE f.workspace_id = $1 AND f.tenant_id = $2 AND f.status = 'active'`,
		workspaceID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("workspaces: access roster grants: %w", err)
	}
	defer fgRows.Close()
	for fgRows.Next() {
		var uid, fid int64
		if err := fgRows.Scan(&uid, &fid); err != nil {
			return nil, fmt.Errorf("workspaces: scan grant: %w", err)
		}
		if i, ok := idx[uid]; ok {
			out[i].FolderIDs = append(out[i].FolderIDs, fid)
		}
	}
	return out, fgRows.Err()
}

// SetMemberAccess sets one member's access to a SINGLE workspace, leaving their
// access to other workspaces untouched. whole=true assigns the whole workspace
// (clearing any folder grants in it); otherwise folderIDs (which must live in
// this workspace) become the grants and any whole assignment is removed.
func (s *Service) SetMemberAccess(ctx context.Context, tenantID, workspaceID, userID int64, whole bool, folderIDs []int64, assignedBy int64) error {
	if _, err := s.Get(ctx, tenantID, workspaceID); err != nil {
		return err
	}
	var membershipID int64
	err := s.client.QueryRowContext(ctx,
		`SELECT id FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
	).Scan(&membershipID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotTenantMember
	}
	if err != nil {
		return fmt.Errorf("workspaces: load membership: %w", err)
	}

	// De-dupe + validate every folder belongs to this workspace.
	uniq := make([]int64, 0, len(folderIDs))
	seen := make(map[int64]bool, len(folderIDs))
	if !whole {
		for _, fid := range folderIDs {
			if seen[fid] {
				continue
			}
			seen[fid] = true
			var ok bool
			if qerr := s.client.QueryRowContext(ctx,
				`SELECT EXISTS(SELECT 1 FROM folders WHERE id = $1 AND tenant_id = $2 AND workspace_id = $3 AND status = 'active')`,
				fid, tenantID, workspaceID,
			).Scan(&ok); qerr != nil {
				return fmt.Errorf("workspaces: validate folder %d: %w", fid, qerr)
			}
			if !ok {
				return ErrNotFound
			}
			uniq = append(uniq, fid)
		}
	}

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("workspaces: begin set access: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Clear this workspace's whole assignment and its folder grants for this user.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userID,
	); err != nil {
		return fmt.Errorf("workspaces: clear whole: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM tenant_member_folders
		 WHERE tenant_member_id = $1
		   AND folder_id IN (SELECT id FROM folders WHERE workspace_id = $2)`,
		membershipID, workspaceID,
	); err != nil {
		return fmt.Errorf("workspaces: clear grants: %w", err)
	}

	if whole {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_members (workspace_id, user_id, assigned_by_user_id)
			VALUES ($1, $2, $3) ON CONFLICT (workspace_id, user_id) DO NOTHING`,
			workspaceID, userID, assignedBy,
		); err != nil {
			return fmt.Errorf("workspaces: assign whole: %w", err)
		}
	} else {
		for _, fid := range uniq {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO tenant_member_folders (tenant_member_id, folder_id)
				VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				membershipID, fid,
			); err != nil {
				return fmt.Errorf("workspaces: insert grant: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("workspaces: commit set access: %w", err)
	}
	committed = true
	logging.Info(packageName, "SetMemberAccess: tenant=%d ws=%d user=%d whole=%v folders=%v",
		tenantID, workspaceID, userID, whole, uniq)
	return nil
}

// IsMemberWhole reports whether the user has a whole-workspace assignment
// (workspace_members row) for the workspace — as opposed to only a folder
// grant inside it. Browse uses this to decide whether to show workspace-root
// files to a scoped member.
func (s *Service) IsMemberWhole(ctx context.Context, workspaceID, userID int64) (bool, error) {
	var whole bool
	if err := s.client.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM workspace_members WHERE workspace_id = $1 AND user_id = $2)`,
		workspaceID, userID,
	).Scan(&whole); err != nil {
		return false, fmt.Errorf("workspaces: whole check: %w", err)
	}
	return whole, nil
}

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
