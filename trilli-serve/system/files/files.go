package files

import (
	"errors"
	"time"
)

const packageName = "files"

// File mirrors the files table. Path is a derived "/folder/subfolder/"
// string populated only by the Recent listing — leave it empty otherwise.
type File struct {
	ID               int64      `json:"id"`
	TenantID         int64      `json:"tenant_id"`
	ParentFolderID   *int64     `json:"parent_folder_id,omitempty"`
	Name             string     `json:"name"`
	SizeBytes        int64      `json:"size_bytes"`
	ContentType      string     `json:"content_type"`
	ProtectedSource  string     `json:"protected_source,omitempty"` // e.g. "trilli-sign": undeletable from Files
	UploadedByUserID int64      `json:"uploaded_by_user_id"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
	Path             string     `json:"path,omitempty"`
	StarredAt        *time.Time `json:"starred_at,omitempty"`
}

// RecentPage is the paginated response from ListRecentPage. Total is the
// active file count across the whole tenant — drives the pagination UI.
type RecentPage struct {
	Files []*File `json:"files"`
	Total int64   `json:"total"`
}

// FileStats is the tenant-wide file count breakdown used to render the
// summary tile on the Recent page.
type FileStats struct {
	Total       int64            `json:"total"`
	ByExtension map[string]int64 `json:"by_extension"`
}

// Quota is what the frontend reads to draw the progress bar.
type Quota struct {
	PlanName         string `json:"plan_name"`
	StorageBytesUsed int64  `json:"storage_bytes_used"`
	MaxStorageBytes  int64  `json:"max_storage_bytes"`
	MaxFileSizeBytes int64  `json:"max_file_size_bytes"`
	// Monthly transfer (bandwidth) usage for the current calendar month.
	// TransferCapBytes is nil when the plan has unlimited transfer.
	TransferUsedBytes int64  `json:"transfer_used_bytes"`
	TransferCapBytes  *int64 `json:"transfer_cap_bytes,omitempty"`
}

// Sentinel errors. Handlers map them to HTTP status codes.
var (
	// ErrFolderProtectedUploads: uploads into the Trilli Sign directory are
	// reserved for Trilli Sign itself.
	ErrFolderProtectedUploads = errors.New("Uploads to the Trilli Sign directory are protected and managed by Trilli Sign")
	// ErrFileProtected: the file is system-managed (e.g. a Trilli Sign
	// deposit) and can only be removed via its managing feature.
	ErrFileProtected          = errors.New("This file is managed by Trilli Sign — to remove it, delete its envelope in Trilli Sign")
	ErrFileTooLarge           = errors.New("files: file exceeds plan max file size")
	ErrQuotaExceeded          = errors.New("files: tenant storage quota exceeded")
	ErrWorkspaceQuotaExceeded = errors.New("files: workspace storage allocation exceeded")
	ErrTransferExceeded       = errors.New("files: monthly transfer limit reached")
	ErrFileNotFound           = errors.New("files: not found")
	ErrInvalidName            = errors.New("files: invalid name")
	ErrNameTaken              = errors.New("files: name already used in this folder")
	ErrInvalidFolder          = errors.New("files: invalid folder")
	ErrWorkspaceNotFound      = errors.New("files: target workspace not found")
)
