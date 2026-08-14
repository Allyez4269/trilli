// Package trash is the read/restore/purge side of soft-delete. Files and
// folders are moved to trash by their own packages (status='trashed', stamped
// with a shared trash_batch); this package lists the batch roots, restores a
// whole batch to its original location, and permanently purges batches —
// deleting blobs + freeing quota — either on demand or via the retention
// janitor once purge_at passes.
package trash

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"
)

const packageName = "trash"

// Retention is how long a trashed item stays restorable before the janitor
// may purge it. Kept here for reference; the actual purge_at is stamped in
// SQL (NOW() + INTERVAL '7 days') by the files/folders trash paths.
const Retention = 7 * 24 * time.Hour

var (
	// ErrBatchNotFound — no trash batch with that id in this tenant.
	ErrBatchNotFound = errors.New("trash: item not found")
)

// Kind of a trash entry's root.
const (
	KindFile   = "file"
	KindFolder = "folder"
)

// Entry is one row in the Trash view — the root of a delete operation. A
// folder delete is a single entry whose size/itemCount cover its whole
// subtree; a file delete is an entry of one.
type Entry struct {
	Batch          int64     `json:"batch"`
	Kind           string    `json:"kind"` // "file" | "folder"
	ID             int64     `json:"id"`   // root file/folder id
	Name           string    `json:"name"`
	Location       string    `json:"location"` // original parent path ("/" = root)
	SizeBytes      int64     `json:"size_bytes"`
	ItemCount      int       `json:"item_count"` // files in the batch (folders only; 0 for a file)
	DeletedAt      time.Time `json:"deleted_at"`
	PurgeAt        time.Time `json:"purge_at"`
	DeletedByEmail string    `json:"deleted_by_email"`
}

// Scope optionally restricts the trash view/actions to a set of folder ids
// (a member's accessible folders). nil = unrestricted (owner/admin). An
// entry matches when its original parent folder is in the set; root-level
// (NULL parent) items are only visible unrestricted.
type Scope struct {
	IDs []int64
	// WorkspaceIDs are whole-workspace assignments: root-level trashed items
	// (parent IS NULL) in these workspaces are within scope even though no
	// parent folder id can match.
	WorkspaceIDs []int64
}

// RootRef identifies a batch's root for access gating before restore/purge.
type RootRef struct {
	Kind           string
	ID             int64
	Name           string
	ParentFolderID *int64
	WorkspaceID    int64 // the root item's workspace — write-gate context for root-level items
}

// int64Slice renders []int64 as a Postgres array literal for ANY($N).
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
