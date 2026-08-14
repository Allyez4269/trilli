// Package supporttickets is the customer support desk: tenant-scoped tickets
// (incidents) plus a threaded conversation per ticket. Customers raise tickets
// and reply from app.trilli.com/support; a future CMS reads these tables to
// triage and post agent replies (PostAgentMessage). Every customer-visible
// write notifies the requester by email, best-effort.
package supporttickets

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"trilli/system/database/postgres"
	"trilli/system/logging"
	"trilli/system/mailer"
)

const packageName = "supporttickets"

// Ticket status lifecycle. open → (agent picks up) pending → awaiting_customer
// ↔ pending → resolved → closed. Customers can close/reopen their own ticket.
const (
	StatusOpen             = "open"
	StatusPending          = "pending"
	StatusAwaitingCustomer = "awaiting_customer"
	StatusResolved         = "resolved"
	StatusClosed           = "closed"
)

// Severity, set by the customer at creation (and adjustable by an agent later).
const (
	SeverityLow    = "low"
	SeverityNormal = "normal"
	SeverityHigh   = "high"
	SeverityUrgent = "urgent"
)

const (
	authorCustomer = "customer"
	authorAgent    = "agent"
	authorSystem   = "system"
)

var (
	validCategories = map[string]bool{
		"billing": true, "technical": true, "account": true,
		"sharing": true, "feature": true, "other": true,
	}
	validSeverities = map[string]bool{
		SeverityLow: true, SeverityNormal: true, SeverityHigh: true, SeverityUrgent: true,
	}
)

var (
	// ErrNotFound is returned when a ticket doesn't exist or isn't visible to
	// the caller (we don't distinguish, to avoid leaking ids across tenants).
	ErrNotFound = errors.New("supporttickets: not found")
	// ErrValidation wraps a user-facing input problem (empty subject, etc.).
	ErrValidation = errors.New("supporttickets: validation")
	// ErrClosed is returned when replying to a closed ticket.
	ErrClosed = errors.New("supporttickets: ticket is closed")
)

// Ticket is one support incident.
type Ticket struct {
	ID             int64      `json:"id"`
	Number         string     `json:"number"`
	TenantID       int64      `json:"-"`
	CreatedByID    int64      `json:"created_by_user_id"`
	RequesterEmail string     `json:"requester_email"`
	RequesterName  string     `json:"requester_name"`
	Subject        string     `json:"subject"`
	Category       string     `json:"category"`
	Severity       string     `json:"severity"`
	Status         string     `json:"status"`
	LastActivityAt time.Time  `json:"last_activity_at"`
	CreatedAt      time.Time  `json:"created_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	MessageCount   int        `json:"message_count"`
	Messages       []Message  `json:"messages,omitempty"`
}

// Message is one entry in a ticket's conversation thread.
type Message struct {
	ID         int64     `json:"id"`
	TicketID   int64     `json:"ticket_id"`
	AuthorType string    `json:"author_type"` // customer | agent | system
	AuthorName string    `json:"author_name"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

// Service is the data layer for support tickets.
type Service struct {
	client *postgres.Client
	mailer *mailer.Mailer
	appURL string // absolute base for ticket links in emails, e.g. https://app.trilli.com
}

// NewService builds the service. mailer may be nil (emails are then skipped).
func NewService(c *postgres.Client, m *mailer.Mailer, appURL string) *Service {
	if strings.TrimSpace(appURL) == "" {
		appURL = "https://app.trilli.com"
	}
	return &Service{client: c, mailer: m, appURL: strings.TrimRight(appURL, "/")}
}

// CreateInput is the payload for a new ticket.
type CreateInput struct {
	TenantID       int64
	UserID         int64
	RequesterEmail string
	RequesterName  string
	Subject        string
	Body           string
	Category       string
	Severity       string
}

// Create opens a new ticket, records the opening message, and emails the
// requester their ticket number. The email failure never fails the create.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Ticket, error) {
	subject := strings.TrimSpace(in.Subject)
	body := strings.TrimSpace(in.Body)
	if subject == "" {
		return nil, fmt.Errorf("%w: subject is required", ErrValidation)
	}
	if body == "" {
		return nil, fmt.Errorf("%w: description is required", ErrValidation)
	}
	if len(subject) > 200 {
		subject = subject[:200]
	}
	category := strings.ToLower(strings.TrimSpace(in.Category))
	if !validCategories[category] {
		category = "other"
	}
	severity := strings.ToLower(strings.TrimSpace(in.Severity))
	if !validSeverities[severity] {
		severity = SeverityNormal
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("supporttickets: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Generate the opaque reference in Go (sk-<MMDDYYYY><9 random digits>),
	// retrying on the rare UNIQUE collision.
	var t *Ticket
	for attempt := 0; attempt < 6; attempt++ {
		ref := newTicketRef()
		t, err = scanTicket(tx.QueryRowContext(ctx, `
			INSERT INTO support_tickets
			    (ticket_number, tenant_id, created_by_user_id, requester_email,
			     requester_name, subject, category, severity)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id, ticket_number, tenant_id, created_by_user_id, requester_email,
			          requester_name, subject, category, severity, status,
			          last_activity_at, created_at, resolved_at, closed_at, 1`,
			ref, in.TenantID, in.UserID, in.RequesterEmail, in.RequesterName,
			subject, category, severity,
		))
		if err == nil {
			break
		}
		if pgUniqueViolation(err) {
			continue // collided on ticket_number — try a fresh ref
		}
		return nil, fmt.Errorf("supporttickets: insert ticket: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("supporttickets: insert ticket (exhausted refs): %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO support_ticket_messages
		    (ticket_id, tenant_id, author_type, author_user_id, author_name, body)
		VALUES ($1, $2, 'customer', $3, $4, $5)`,
		t.ID, in.TenantID, in.UserID, in.RequesterName, body,
	); err != nil {
		return nil, fmt.Errorf("supporttickets: insert opening message: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("supporttickets: commit: %w", err)
	}
	committed = true

	logging.Info(packageName, "ticket created: %s tenant=%d user=%d severity=%s", t.Number, in.TenantID, in.UserID, severity)

	if s.mailer != nil {
		if err := s.mailer.SendTicketCreated(ctx, mailer.TicketCreatedEmail{
			To:       t.RequesterEmail,
			Name:     t.RequesterName,
			Number:   t.Number,
			Subject:  t.Subject,
			Severity: t.Severity,
			Link:     s.ticketLink(t.Number),
		}); err != nil {
			logging.Error(packageName, "send ticket-created email for %s: %v", t.Number, err)
		}
	}
	return t, nil
}

// List returns a tenant's tickets, most-recently-active first. When allTenant
// is false, only tickets opened by userID are returned (the member view).
func (s *Service) List(ctx context.Context, tenantID, userID int64, allTenant bool) ([]Ticket, error) {
	q := `
		SELECT t.id, t.ticket_number, t.tenant_id, t.created_by_user_id, t.requester_email,
		       t.requester_name, t.subject, t.category, t.severity, t.status,
		       t.last_activity_at, t.created_at, t.resolved_at, t.closed_at,
		       (SELECT COUNT(*) FROM support_ticket_messages m
		          WHERE m.ticket_id = t.id AND m.is_internal = FALSE) AS message_count
		  FROM support_tickets t
		 WHERE t.tenant_id = $1`
	args := []any{tenantID}
	if !allTenant {
		q += ` AND t.created_by_user_id = $2`
		args = append(args, userID)
	}
	q += ` ORDER BY t.last_activity_at DESC, t.id DESC`

	rows, err := s.client.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("supporttickets: list: %w", err)
	}
	defer rows.Close()

	out := []Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Get returns one ticket (by its opaque reference, e.g. sk-06112026487193562)
// with its full customer-visible conversation thread. Visibility: the ticket's
// tenant must match, and unless allTenant the caller must be the creator.
func (s *Service) Get(ctx context.Context, tenantID, userID int64, ref string, allTenant bool) (*Ticket, error) {
	q := `
		SELECT t.id, t.ticket_number, t.tenant_id, t.created_by_user_id, t.requester_email,
		       t.requester_name, t.subject, t.category, t.severity, t.status,
		       t.last_activity_at, t.created_at, t.resolved_at, t.closed_at, 0
		  FROM support_tickets t
		 WHERE t.ticket_number = $1 AND t.tenant_id = $2`
	args := []any{ref, tenantID}
	if !allTenant {
		q += ` AND t.created_by_user_id = $3`
		args = append(args, userID)
	}

	t, err := scanTicket(s.client.QueryRowContext(ctx, q, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("supporttickets: get: %w", err)
	}

	msgs, err := s.messages(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	t.Messages = msgs
	t.MessageCount = len(msgs)
	return t, nil
}

func (s *Service) messages(ctx context.Context, ticketID int64) ([]Message, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT id, ticket_id, author_type, author_name, body, created_at
		  FROM support_ticket_messages
		 WHERE ticket_id = $1 AND is_internal = FALSE
		 ORDER BY created_at ASC, id ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("supporttickets: messages: %w", err)
	}
	defer rows.Close()

	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.TicketID, &m.AuthorType, &m.AuthorName, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddCustomerMessage appends a customer reply and reopens an awaiting/resolved
// ticket. Returns the refreshed ticket (with thread).
func (s *Service) AddCustomerMessage(ctx context.Context, tenantID, userID int64, ref string, allTenant bool, name, body string) (*Ticket, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("%w: message is required", ErrValidation)
	}
	// Confirm visibility + current status first.
	t, err := s.Get(ctx, tenantID, userID, ref, allTenant)
	if err != nil {
		return nil, err
	}
	if t.Status == StatusClosed {
		return nil, ErrClosed
	}

	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO support_ticket_messages
		    (ticket_id, tenant_id, author_type, author_user_id, author_name, body)
		VALUES ($1, $2, 'customer', $3, $4, $5)`,
		t.ID, tenantID, userID, name, body,
	); err != nil {
		return nil, fmt.Errorf("supporttickets: insert customer message: %w", err)
	}

	// A customer reply on an awaiting/resolved ticket puts the ball back in
	// support's court (→ pending). Otherwise just bump activity.
	newStatus := t.Status
	if t.Status == StatusAwaitingCustomer || t.Status == StatusResolved {
		newStatus = StatusPending
	}
	if _, err := s.client.ExecContext(ctx, `
		UPDATE support_tickets
		   SET status = $2, last_activity_at = NOW(), updated_at = NOW(),
		       resolved_at = CASE WHEN $2 <> 'resolved' THEN NULL ELSE resolved_at END
		 WHERE id = $1`, t.ID, newStatus); err != nil {
		return nil, fmt.Errorf("supporttickets: bump ticket: %w", err)
	}

	logging.Info(packageName, "customer reply on %s (status %s→%s)", t.Number, t.Status, newStatus)
	return s.Get(ctx, tenantID, userID, ref, allTenant)
}

// SetStatus transitions a ticket (customer-initiated close/reopen). Stamps
// resolved_at/closed_at as appropriate.
func (s *Service) SetStatus(ctx context.Context, tenantID, userID int64, ref string, allTenant bool, status string) (*Ticket, error) {
	switch status {
	case StatusOpen, StatusPending, StatusAwaitingCustomer, StatusResolved, StatusClosed:
	default:
		return nil, fmt.Errorf("%w: unknown status", ErrValidation)
	}
	// Visibility check + resolve the internal id.
	t, err := s.Get(ctx, tenantID, userID, ref, allTenant)
	if err != nil {
		return nil, err
	}

	res, err := s.client.ExecContext(ctx, `
		UPDATE support_tickets
		   SET status = $2,
		       last_activity_at = NOW(),
		       updated_at = NOW(),
		       resolved_at = CASE WHEN $2 = 'resolved' THEN NOW()
		                          WHEN $2 IN ('open','pending','awaiting_customer') THEN NULL
		                          ELSE resolved_at END,
		       closed_at   = CASE WHEN $2 = 'closed' THEN NOW()
		                          WHEN $2 <> 'closed' THEN NULL
		                          ELSE closed_at END
		 WHERE id = $1 AND tenant_id = $3`, t.ID, status, tenantID)
	if err != nil {
		return nil, fmt.Errorf("supporttickets: set status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}

	// Record a system note in the thread so the transition is visible.
	_, _ = s.client.ExecContext(ctx, `
		INSERT INTO support_ticket_messages (ticket_id, tenant_id, author_type, author_name, body)
		VALUES ($1, $2, 'system', 'Trilli', $3)`,
		t.ID, tenantID, statusNote(status))

	logging.Info(packageName, "ticket %s status → %s", t.Number, status)
	return s.Get(ctx, tenantID, userID, ref, allTenant)
}

// PostAgentMessage is the primitive the future support CMS calls to answer a
// ticket from the backend. It appends an agent reply, moves the ticket to
// awaiting_customer (unless resolving/closing), and emails the requester that
// there's an update. tenantID is looked up from the ticket so callers needn't
// know it. Not wired to any customer-facing route.
func (s *Service) PostAgentMessage(ctx context.Context, ticketID int64, agentName, body, newStatus string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("%w: message is required", ErrValidation)
	}
	if agentName == "" {
		agentName = "Trilli Support"
	}

	var tenantID int64
	var number, subject, email, reqName string
	err := s.client.QueryRowContext(ctx, `
		SELECT tenant_id, ticket_number, subject, requester_email, requester_name
		  FROM support_tickets WHERE id = $1`, ticketID,
	).Scan(&tenantID, &number, &subject, &email, &reqName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("supporttickets: load ticket for agent reply: %w", err)
	}

	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO support_ticket_messages (ticket_id, tenant_id, author_type, author_name, body)
		VALUES ($1, $2, 'agent', $3, $4)`, ticketID, tenantID, agentName, body); err != nil {
		return fmt.Errorf("supporttickets: insert agent message: %w", err)
	}

	status := newStatus
	switch status {
	case StatusPending, StatusAwaitingCustomer, StatusResolved, StatusClosed:
	default:
		status = StatusAwaitingCustomer
	}
	if _, err := s.client.ExecContext(ctx, `
		UPDATE support_tickets
		   SET status = $2, last_activity_at = NOW(), updated_at = NOW(),
		       resolved_at = CASE WHEN $2 = 'resolved' THEN NOW() ELSE resolved_at END,
		       closed_at   = CASE WHEN $2 = 'closed' THEN NOW() ELSE closed_at END
		 WHERE id = $1`, ticketID, status); err != nil {
		return fmt.Errorf("supporttickets: bump after agent reply: %w", err)
	}

	logging.Info(packageName, "agent reply on %s (status → %s)", number, status)

	if s.mailer != nil {
		if err := s.mailer.SendTicketUpdate(ctx, mailer.TicketUpdateEmail{
			To:      email,
			Name:    reqName,
			Number:  number,
			Subject: subject,
			Status:  status,
			Link:    s.ticketLink(number),
		}); err != nil {
			logging.Error(packageName, "send ticket-update email for %s: %v", number, err)
		}
	}
	return nil
}

// PostInternalNote appends an operator-only note to a ticket (is_internal=TRUE):
// never shown to the customer, no email, no status change — the triage
// annotation support staff leave for each other (SPEC §6.8). tenantID is looked
// up from the ticket.
func (s *Service) PostInternalNote(ctx context.Context, ticketID int64, authorName, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("%w: note is required", ErrValidation)
	}
	if authorName == "" {
		authorName = "Operator"
	}
	var tenantID int64
	if err := s.client.QueryRowContext(ctx,
		`SELECT tenant_id FROM support_tickets WHERE id = $1`, ticketID).Scan(&tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("supporttickets: load ticket for note: %w", err)
	}
	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO support_ticket_messages (ticket_id, tenant_id, author_type, author_name, body, is_internal)
		VALUES ($1, $2, 'agent', $3, $4, TRUE)`, ticketID, tenantID, authorName, body); err != nil {
		return fmt.Errorf("supporttickets: insert internal note: %w", err)
	}
	// Bump activity so it surfaces in triage; status is intentionally untouched.
	_, _ = s.client.ExecContext(ctx,
		`UPDATE support_tickets SET last_activity_at = NOW(), updated_at = NOW() WHERE id = $1`, ticketID)
	logging.Info(packageName, "internal note on ticket id=%d by %s", ticketID, authorName)
	return nil
}

func (s *Service) ticketLink(ref string) string {
	return fmt.Sprintf("%s/support/%s", s.appURL, ref)
}

// newTicketRef builds an opaque, date-stamped reference:
// sk-<MMDDYYYY><9 random digits>, e.g. sk-06112026487193562. The date is the
// creation date; the random tail makes it non-enumerable.
func newTicketRef() string {
	const prefix = "sk-"
	date := time.Now().Format("01022006") // MMDDYYYY
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000_000))
	if err != nil {
		// crypto/rand should never fail; fall back to a time-derived tail.
		n = big.NewInt(time.Now().UnixNano() % 1_000_000_000)
	}
	return fmt.Sprintf("%s%s%09d", prefix, date, n.Int64())
}

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// --- scan helpers ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTicket(r rowScanner) (*Ticket, error) {
	var t Ticket
	var resolved, closed sql.NullTime
	if err := r.Scan(
		&t.ID, &t.Number, &t.TenantID, &t.CreatedByID, &t.RequesterEmail,
		&t.RequesterName, &t.Subject, &t.Category, &t.Severity, &t.Status,
		&t.LastActivityAt, &t.CreatedAt, &resolved, &closed,
		&t.MessageCount,
	); err != nil {
		return nil, err
	}
	if resolved.Valid {
		t.ResolvedAt = &resolved.Time
	}
	if closed.Valid {
		t.ClosedAt = &closed.Time
	}
	return &t, nil
}

func statusNote(status string) string {
	switch status {
	case StatusClosed:
		return "Ticket closed."
	case StatusResolved:
		return "Ticket marked as resolved."
	case StatusOpen, StatusPending:
		return "Ticket reopened."
	default:
		return "Ticket status updated."
	}
}
