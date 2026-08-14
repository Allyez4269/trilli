// Package support gives CMX operators a cross-tenant view of the app's support
// desk (SPEC §6.8): a triage list over every tenant's tickets and a ticket
// thread including operator-only internal notes. Reads hit the shared
// support_tickets / support_ticket_messages tables directly (Option C); replies
// and notes are WRITES proxied to the app via appadmin (they email the customer
// and mutate status), recorded in the CMX operator audit.
package support

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"trilli-cmx/system/database/postgres"
)

const packageName = "support"

// ErrNotFound is returned when a ticket does not exist.
var ErrNotFound = errors.New("support: ticket not found")

// Service runs support read queries.
type Service struct {
	db *postgres.Client
}

// NewService constructs the support Service.
func NewService(db *postgres.Client) *Service { return &Service{db: db} }

// TicketListItem is one row in the cross-tenant triage list.
type TicketListItem struct {
	ID             int64  `json:"id"`
	Number         string `json:"number"`
	TenantID       int64  `json:"tenant_id"`
	TenantName     string `json:"tenant_name"`
	RequesterEmail string `json:"requester_email"`
	RequesterName  string `json:"requester_name"`
	Subject        string `json:"subject"`
	Category       string `json:"category"`
	Severity       string `json:"severity"`
	Status         string `json:"status"`
	MessageCount   int    `json:"message_count"`
	LastActivityAt string `json:"last_activity_at"`
	CreatedAt      string `json:"created_at"`
}

// Message is one thread entry. IsInternal notes are operator-only.
type Message struct {
	ID         int64  `json:"id"`
	AuthorType string `json:"author_type"` // customer | agent | system
	AuthorName string `json:"author_name"`
	Body       string `json:"body"`
	IsInternal bool   `json:"is_internal"`
	CreatedAt  string `json:"created_at"`
}

// TicketDetail is a ticket with its full thread (internal notes included).
type TicketDetail struct {
	TicketListItem
	Messages []Message `json:"messages"`
}

const ticketCols = `
	t.id, t.ticket_number, t.tenant_id, COALESCE(tn.name,''),
	t.requester_email, t.requester_name, t.subject, t.category, t.severity, t.status,
	(SELECT COUNT(*) FROM support_ticket_messages m WHERE m.ticket_id = t.id AND m.is_internal = FALSE),
	to_char(t.last_activity_at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
	to_char(t.created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')`

func scanTicket(sc interface{ Scan(...any) error }, t *TicketListItem) error {
	return sc.Scan(&t.ID, &t.Number, &t.TenantID, &t.TenantName,
		&t.RequesterEmail, &t.RequesterName, &t.Subject, &t.Category, &t.Severity, &t.Status,
		&t.MessageCount, &t.LastActivityAt, &t.CreatedAt)
}

// ListTickets returns tickets across every tenant, most-recently-active first.
// status filters by lifecycle state ("" = all, "open_active" = anything not
// resolved/closed); q matches number / subject / requester email (case-insensitive).
func (s *Service) ListTickets(ctx context.Context, status, q string) ([]TicketListItem, error) {
	var where []string
	var args []any
	switch status {
	case "", "all":
	case "open_active":
		where = append(where, "t.status NOT IN ('resolved','closed')")
	default:
		args = append(args, status)
		where = append(where, fmt.Sprintf("t.status = $%d", len(args)))
	}
	if qq := strings.TrimSpace(q); qq != "" {
		args = append(args, "%"+strings.ToLower(qq)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(LOWER(t.ticket_number) LIKE $%d OR LOWER(t.subject) LIKE $%d OR LOWER(t.requester_email) LIKE $%d)", n, n, n))
	}
	query := `SELECT ` + ticketCols + ` FROM support_tickets t LEFT JOIN tenants tn ON tn.id = t.tenant_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY t.last_activity_at DESC, t.id DESC LIMIT 500"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("support: list tickets: %w", err)
	}
	defer rows.Close()
	var out []TicketListItem
	for rows.Next() {
		var t TicketListItem
		if err := scanTicket(rows, &t); err != nil {
			return nil, fmt.Errorf("support: scan ticket: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTicket returns one ticket (by numeric id) with its full thread, including
// operator-only internal notes.
func (s *Service) GetTicket(ctx context.Context, id int64) (*TicketDetail, error) {
	var d TicketDetail
	err := scanTicket(s.db.QueryRowContext(ctx,
		`SELECT `+ticketCols+` FROM support_tickets t LEFT JOIN tenants tn ON tn.id = t.tenant_id WHERE t.id = $1`, id),
		&d.TicketListItem)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("support: get ticket: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, author_type, COALESCE(author_name,''), body, is_internal,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		  FROM support_ticket_messages WHERE ticket_id = $1 ORDER BY created_at ASC, id ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("support: load messages: %w", err)
	}
	defer rows.Close()
	d.Messages = []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.AuthorType, &m.AuthorName, &m.Body, &m.IsInternal, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("support: scan message: %w", err)
		}
		d.Messages = append(d.Messages, m)
	}
	return &d, rows.Err()
}
