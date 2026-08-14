package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"trilli/system/database/postgres"
	"trilli/system/logging"
)

// Service writes and reads audit events.
type Service struct {
	client *postgres.Client
}

// NewService returns a wired audit Service.
func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

// RecordInput is one event to append. ActorUserID 0 stores NULL (system
// action). Path fields are pre-resolved by the caller (see PathOf).
type RecordInput struct {
	TenantID    int64
	ActorUserID int64
	ActorEmail  string
	Action      string
	TargetType  string
	TargetID    *int64
	ItemName    string
	ItemPath    string
	FromPath    *string
	ToPath      *string
	OldName     *string
	Device      string
	IP          string
}

// Record appends one event. It is best-effort: a failure is logged but
// never returned, so an audit hiccup can't break the user action that
// triggered it. (If hard durability is ever required, fold this into the
// action's transaction instead.)
func (s *Service) Record(ctx context.Context, in RecordInput) {
	var actor any
	if in.ActorUserID != 0 {
		actor = in.ActorUserID
	}
	_, err := s.client.ExecContext(ctx, `
		INSERT INTO audit_events
		    (tenant_id, actor_user_id, actor_email, action, target_type, target_id,
		     item_name, item_path, from_path, to_path, old_name, device, ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		in.TenantID, actor, in.ActorEmail, in.Action, in.TargetType, in.TargetID,
		in.ItemName, nullIfEmpty(in.ItemPath), in.FromPath, in.ToPath, in.OldName,
		nullIfEmpty(in.Device), nullIfEmpty(in.IP),
	)
	if err != nil {
		logging.Error(packageName, "Record failed (tenant=%d action=%s item=%q): %v",
			in.TenantID, in.Action, in.ItemName, err)
	}
}

// PathOf resolves a folder id to a display breadcrumb path ("/A/B"), or "/"
// for the tenant root (nil). Best-effort: any error yields "/". Computed at
// record time so the snapshot reflects the structure as it was.
func (s *Service) PathOf(ctx context.Context, tenantID int64, folderID *int64) string {
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
		logging.Debug(packageName, "PathOf failed for folder=%d: %v", *folderID, err)
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

// List returns a tenant's events newest-first, filtered + paginated.
func (s *Service) List(ctx context.Context, tenantID int64, f ListFilter) (*ListResult, error) {
	where := []string{"a.tenant_id = $1"}
	args := []any{tenantID}
	n := 1

	switch f.Range {
	case "today":
		where = append(where, "a.created_at >= date_trunc('day', now())")
	case "7d":
		where = append(where, "a.created_at >= now() - interval '7 days'")
	case "30d":
		where = append(where, "a.created_at >= now() - interval '30 days'")
		// "all" / "" → no date constraint
	}

	if f.Action != "" && f.Action != "all" {
		n++
		args = append(args, f.Action)
		where = append(where, fmt.Sprintf("a.action = $%d", n))
	}

	// Scope to one actor's own events (a non-admin member viewing activity).
	if f.ActorUserID != nil {
		n++
		args = append(args, *f.ActorUserID)
		where = append(where, fmt.Sprintf("a.actor_user_id = $%d", n))
	}

	if q := strings.TrimSpace(f.Query); q != "" {
		n++
		args = append(args, "%"+q+"%")
		where = append(where, fmt.Sprintf(
			"(a.item_name ILIKE $%d OR a.item_path ILIKE $%d OR a.actor_email ILIKE $%d OR COALESCE(u.full_name,'') ILIKE $%d)",
			n, n, n, n))
	}

	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_events a
		   LEFT JOIN users u ON u.id = a.actor_user_id
		  WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("audit: count: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	n++
	limitParam := n
	args = append(args, limit)
	n++
	offsetParam := n
	args = append(args, offset)

	rows, err := s.client.QueryContext(ctx, fmt.Sprintf(`
		SELECT a.id, a.actor_user_id, a.actor_email, COALESCE(u.full_name, ''),
		       u.avatar_key, u.avatar_updated_at,
		       a.action, a.target_type, a.target_id, a.item_name, COALESCE(a.item_path, ''),
		       a.from_path, a.to_path, a.old_name, COALESCE(a.device, ''), COALESCE(a.ip, ''),
		       a.created_at,
		       (a.target_type <> 'file' OR (f.id IS NOT NULL AND f.deleted_at IS NULL)) AS still_exists,
		       COALESCE(f.content_type, '') AS content_type
		  FROM audit_events a
		  LEFT JOIN users u ON u.id = a.actor_user_id
		  LEFT JOIN files f ON f.id = a.target_id AND a.target_type = 'file'
		 WHERE %s
		 ORDER BY a.created_at DESC, a.id DESC
		 LIMIT $%d OFFSET $%d`, whereSQL, limitParam, offsetParam), args...)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()

	events := make([]*Event, 0, limit)
	for rows.Next() {
		var (
			e             Event
			fullName      string
			avatarKey     sql.NullString
			avatarUpdated sql.NullTime
		)
		if err := rows.Scan(&e.ID, &e.ActorUserID, &e.ActorEmail, &fullName,
			&avatarKey, &avatarUpdated,
			&e.Action, &e.TargetType, &e.TargetID, &e.ItemName, &e.ItemPath,
			&e.FromPath, &e.ToPath, &e.OldName, &e.Device, &e.IP, &e.CreatedAt,
			&e.StillExists, &e.ContentType); err != nil {
			return nil, fmt.Errorf("audit: scan: %w", err)
		}
		if strings.TrimSpace(fullName) != "" {
			e.ActorName = fullName
		} else {
			e.ActorName = e.ActorEmail
		}
		// Build the tenant-gated avatar URL (matches setAvatarURL) when the
		// actor has an uploaded avatar; otherwise the UI falls back to initials.
		if e.ActorUserID != nil && avatarKey.Valid && strings.TrimSpace(avatarKey.String) != "" {
			v := int64(0)
			if avatarUpdated.Valid {
				v = avatarUpdated.Time.Unix()
			}
			e.ActorAvatarURL = fmt.Sprintf("/api/users/%d/avatar?v=%d", *e.ActorUserID, v)
		}
		events = append(events, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: rows: %w", err)
	}
	return &ListResult{Events: events, Total: total}, nil
}

// DeviceFromUA classifies a User-Agent string into a coarse device label.
// "App" (native client) needs an explicit client signal we don't emit yet,
// so the web app resolves to Desktop or Mobile.
func DeviceFromUA(ua string) string {
	l := strings.ToLower(ua)
	for _, m := range []string{"mobile", "iphone", "android", "ipad", "ipod"} {
		if strings.Contains(l, m) {
			return "Mobile"
		}
	}
	return "Desktop"
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
