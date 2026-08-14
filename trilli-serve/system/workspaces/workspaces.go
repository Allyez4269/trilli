// Package workspaces is the new layer between an Account (tenant) and its
// folders/files. An account can have many workspaces; each holds its own
// folders/files and carries a disk allocation drawn from the account's plan
// quota. Owners/admins manage all workspaces in the account; members/viewers
// are assigned to one or more workspaces (whole-workspace access).
package workspaces

import (
	"errors"
	"time"
)

const packageName = "workspaces"

// Sentinel errors mapped to HTTP statuses by the handlers.
var (
	ErrNotFound         = errors.New("workspaces: workspace not found")
	ErrNameRequired     = errors.New("workspaces: workspace name is required")
	ErrNameTaken        = errors.New("workspaces: a workspace with that name already exists")
	ErrAllocInvalid     = errors.New("workspaces: invalid disk allocation")
	ErrAllocExceedsPool = errors.New("workspaces: allocation exceeds the account's available storage")
	ErrAllocBelowUsage  = errors.New("workspaces: allocation is below what the workspace already uses")
	ErrNotTenantMember  = errors.New("workspaces: user is not a member of this account")
	ErrForbidden        = errors.New("workspaces: not allowed")
	ErrWorkspaceLimit   = errors.New("workspaces: your plan's workspace limit is reached — upgrade to add more")
)

// Workspace mirrors a row in the workspaces table.
type Workspace struct {
	ID                  int64     `json:"id"`
	TenantID            int64     `json:"tenant_id"`
	Name                string    `json:"name"`
	DiskAllocationBytes int64     `json:"disk_allocation_bytes"`
	StorageBytesUsed    int64     `json:"storage_bytes_used"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
}

// AccountQuota summarizes the account's storage pool so the create-workspace
// UI can show how much is left to allocate.
type AccountQuota struct {
	TotalBytes     int64 `json:"total_bytes"`     // the account plan's max storage
	AllocatedBytes int64 `json:"allocated_bytes"` // sum of active workspace allocations
	RemainingBytes int64 `json:"remaining_bytes"` // total - allocated (>= 0)
}

// Member is one user assigned to a workspace.
type Member struct {
	UserID     int64     `json:"user_id"`
	Email      string    `json:"email"`
	FullName   *string   `json:"full_name,omitempty"`
	AssignedAt time.Time `json:"assigned_at"`
}

// MemberAccess summarizes one member/viewer's access to a specific workspace:
// whole-workspace assignment and/or the folder grants they hold inside it.
type MemberAccess struct {
	UserID    int64   `json:"user_id"`
	Email     string  `json:"email"`
	FullName  *string `json:"full_name,omitempty"`
	Role      string  `json:"role"`
	Whole     bool    `json:"whole"`
	FolderIDs []int64 `json:"folder_ids"`
}
