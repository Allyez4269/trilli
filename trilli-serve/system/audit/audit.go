// Package audit is the append-only activity log. Every mutating action on
// a file or folder (plus downloads) records one immutable event; the
// Activity page reads them back, filtered and paginated. The table's
// immutability is enforced by a DB trigger (see migration 000014) — this
// package only ever INSERTs and SELECTs.
package audit

import "time"

const packageName = "audit"

// Action values. Kept as plain string constants so they match the
// frontend's ActionKey union 1:1.
const (
	ActionUpload       = "upload"
	ActionDownload     = "download"
	ActionMove         = "move"
	ActionCopy         = "copy"
	ActionRename       = "rename"
	ActionDelete       = "delete"
	ActionRestore      = "restore"
	ActionCreateFolder = "create_folder"
	ActionShare        = "share"
	ActionStar         = "star"
	ActionView         = "view"
	// Account-level lifecycle events (owner-initiated).
	ActionAccountDelete     = "account_delete"
	ActionAccountReactivate = "account_reactivate"
)

// Target types.
const (
	TargetFile    = "file"
	TargetFolder  = "folder"
	TargetAccount = "account"
)

// Event is one audit row as returned to the Activity API. Path/name fields
// are snapshots taken at write time, so the log reads correctly even after
// the underlying file/folder is renamed, moved, or deleted.
type Event struct {
	ID             int64  `json:"id"`
	ActorUserID    *int64 `json:"actor_user_id,omitempty"`
	ActorName      string `json:"actor_name"` // full_name if set, else email
	ActorEmail     string `json:"actor_email"`
	ActorAvatarURL string `json:"actor_avatar_url,omitempty"` // uploaded avatar, if any
	Action         string `json:"action"`
	TargetType     string `json:"target_type"`
	TargetID       *int64 `json:"target_id,omitempty"`
	ItemName       string `json:"item_name"`
	ItemPath       string `json:"item_path"`
	// StillExists is true unless this is a file event whose referenced file has
	// since been trashed or purged. For non-file targets it is always true. Lets
	// the UI disable links and the right-click menu for files that are gone.
	StillExists bool `json:"still_exists"`
	// ContentType is the referenced file's MIME when it still exists, so the UI
	// can decide whether a preview is even possible.
	ContentType string    `json:"content_type,omitempty"`
	FromPath    *string   `json:"from_path,omitempty"`
	ToPath      *string   `json:"to_path,omitempty"`
	OldName     *string   `json:"old_name,omitempty"`
	Device      string    `json:"device"`
	IP          string    `json:"ip"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListResult is the paginated read shape for GET /api/activity.
type ListResult struct {
	Events []*Event `json:"events"`
	Total  int64    `json:"total"`
}

// ListFilter mirrors the Activity page's controls. Empty/zero fields mean
// "no constraint".
type ListFilter struct {
	Range       string // "today" | "7d" | "30d" | "all" (default: all)
	Action      string // "" or "all" = any; else a specific Action value
	Query       string // substring match on item name/path, actor email/name
	ActorUserID *int64 // non-nil scopes to one actor's own events (members)
	Offset      int
	Limit       int
}
