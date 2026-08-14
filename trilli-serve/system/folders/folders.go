package folders

import (
	"errors"
	"time"
)

const packageName = "folders"

// Folder mirrors the folders table. SizeBytes is a computed total of all
// active files anywhere inside the subtree (populated only by listing paths
// that ask for it — Get/Create/etc leave it zero).
type Folder struct {
	ID              int64      `json:"id"`
	TenantID        int64      `json:"tenant_id"`
	ParentFolderID  *int64     `json:"parent_folder_id,omitempty"`
	Name            string     `json:"name"`
	CreatedByUserID int64      `json:"created_by_user_id"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	StarredAt       *time.Time `json:"starred_at,omitempty"`
	WorkspaceID     int64      `json:"workspace_id,omitempty"` // populated by ListStarred (for navigation)
	SizeBytes       int64      `json:"size_bytes"`
	ItemCount       int        `json:"item_count"`
	// FileCount is the number of files directly in this folder (this level only;
	// subfolders and their contents are not counted). Drives the row count
	// badge — shown only when files actually exist in the folder.
	FileCount int `json:"file_count"`
}

// BreadcrumbItem is a step in a folder's path, root → leaf.
type BreadcrumbItem struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// "trilli-sign" when this crumb is a system folder — the UI disables
	// uploads anywhere inside a protected lineage.
	ProtectedSource string `json:"protected_source,omitempty"`
}

// Sentinel errors. Handlers map them to HTTP statuses.
var (
	ErrFolderNotFound = errors.New("folders: not found")
	// ErrContainsProtected: the subtree holds Trilli Sign-managed files.
	ErrContainsProtected = errors.New("This folder contains signed agreements managed by Trilli Sign — remove them by deleting their envelopes in Trilli Sign first")
	ErrInvalidName       = errors.New("folders: invalid name")
	ErrNameTaken         = errors.New("folders: name already used in this location")
	ErrCycle             = errors.New("folders: move would create a cycle")
	ErrInvalidParent     = errors.New("folders: invalid parent")
	ErrInvalidWorkspace  = errors.New("folders: invalid workspace")
)
