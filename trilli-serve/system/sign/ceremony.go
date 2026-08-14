package sign

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"trilli/system/logging"
	"trilli/system/mailer"
)

// The signing ceremony: everything a recipient does with their token. These
// paths are PUBLIC (no session) — possession of the unguessable token is the
// access, the same trust model as share links and drop portals.

var (
	ErrNotSignable   = errors.New("sign: this envelope is not open for signing")
	ErrAlreadySigned = errors.New("sign: you have already signed this document")
	ErrMissingFields = errors.New("sign: complete every required field before finishing")
	ErrNoConsent     = errors.New("sign: consent is required to sign electronically")
	ErrBadSignature  = errors.New("sign: a signature is required")
)

// CeremonyView is the full signer-facing payload for an open envelope.
type CeremonyView struct {
	Title         string     `json:"title"`
	Message       string     `json:"message"`
	SenderName    string     `json:"sender_name"`
	PageCount     int        `json:"page_count"`
	RecipientName string     `json:"recipient_name"`
	RecipientMail string     `json:"recipient_email"`
	TotalSigners  int        `json:"total_signers"`
	SignedCount   int        `json:"signed_count"`
	Status        string     `json:"status"`          // recipient status
	EnvelopeState string     `json:"envelope_status"` // draft|sent|completed|voided|declined
	SignedAt      *time.Time `json:"signed_at,omitempty"`
	Fields        []*Field   `json:"fields"` // ONLY this recipient's fields
}

type ceremonyRow struct {
	recipientID int64
	envelopeID  int64
	tenantID    int64
	recStatus   string
	envStatus   string
}

func (s *Service) ceremonyRow(ctx context.Context, token string) (*ceremonyRow, error) {
	token = strings.TrimSpace(token)
	if len(token) < 20 {
		return nil, ErrTokenNotFound
	}
	var c ceremonyRow
	err := s.pg.QueryRowContext(ctx, `
		SELECT r.id, r.envelope_id, e.tenant_id, r.status, e.status
		  FROM sign_recipients r JOIN sign_envelopes e ON e.id = r.envelope_id
		 WHERE r.token = $1`, token,
	).Scan(&c.recipientID, &c.envelopeID, &c.tenantID, &c.recStatus, &c.envStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CeremonyView resolves the full signer payload and records the first view.
func (s *Service) CeremonyViewFull(ctx context.Context, token string) (*CeremonyView, error) {
	c, err := s.ceremonyRow(ctx, token)
	if err != nil {
		return nil, err
	}
	var v CeremonyView
	var senderName, senderEmail sql.NullString
	err = s.pg.QueryRowContext(ctx, `
		SELECT e.title, e.message, e.page_count, e.status, r.name, r.email, r.status, r.signed_at,
		       u.full_name, u.email
		  FROM sign_envelopes e
		  JOIN sign_recipients r ON r.envelope_id = e.id AND r.id = $2
		  LEFT JOIN users u ON u.id = e.created_by_user_id
		 WHERE e.id = $1`, c.envelopeID, c.recipientID,
	).Scan(&v.Title, &v.Message, &v.PageCount, &v.EnvelopeState, &v.RecipientName, &v.RecipientMail,
		&v.Status, &v.SignedAt, &senderName, &senderEmail)
	if err != nil {
		return nil, err
	}
	_ = s.pg.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status = 'signed')
		  FROM sign_recipients WHERE envelope_id = $1`, c.envelopeID,
	).Scan(&v.TotalSigners, &v.SignedCount)
	if senderName.Valid && senderName.String != "" {
		v.SenderName = senderName.String
	} else if senderEmail.Valid {
		v.SenderName = senderEmail.String
	}

	rows, err := s.pg.QueryContext(ctx, `
		SELECT id, recipient_id, kind, page, x, y, w, h, required, meta, COALESCE(value, '')
		  FROM sign_fields WHERE envelope_id = $1 AND recipient_id = $2 ORDER BY page, id`,
		c.envelopeID, c.recipientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	v.Fields = []*Field{}
	for rows.Next() {
		var f Field
		if err := rows.Scan(&f.ID, &f.RecipientID, &f.Kind, &f.Page, &f.X, &f.Y, &f.W, &f.H, &f.Required, &f.Meta, &f.Value); err != nil {
			return nil, err
		}
		v.Fields = append(v.Fields, &f)
	}

	// First open: notified -> viewed, on the record.
	if v.EnvelopeState == "sent" && (c.recStatus == "notified" || c.recStatus == "pending") {
		if _, err := s.pg.ExecContext(ctx, `
			UPDATE sign_recipients SET status = 'viewed', viewed_at = COALESCE(viewed_at, now())
			 WHERE id = $1 AND status IN ('pending','notified')`, c.recipientID); err == nil {
			s.event(ctx, c.envelopeID, "signer", "viewed", v.RecipientName)
			v.Status = "viewed"
		}
	}
	return &v, nil
}

// PreviewCeremony is the sender's dry run: the exact CeremonyView a recipient
// would see, WITHOUT sending, recording events, or requiring a token. The
// envelope state is presented as 'sent' so every ceremony interaction lights
// up; nothing the previewer does is persisted.
func (s *Service) PreviewCeremony(ctx context.Context, tenantID, envelopeID, recipientID int64) (*CeremonyView, error) {
	var v CeremonyView
	var senderName, senderEmail sql.NullString
	err := s.pg.QueryRowContext(ctx, `
		SELECT e.title, e.message, e.page_count, u.full_name, u.email
		  FROM sign_envelopes e
		  LEFT JOIN users u ON u.id = e.created_by_user_id
		 WHERE e.id = $1 AND e.tenant_id = $2`, envelopeID, tenantID,
	).Scan(&v.Title, &v.Message, &v.PageCount, &senderName, &senderEmail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if senderName.Valid && senderName.String != "" {
		v.SenderName = senderName.String
	} else if senderEmail.Valid {
		v.SenderName = senderEmail.String
	}
	// resolve the recipient (explicit id, else first in signing order)
	q := `SELECT id, name FROM sign_recipients WHERE envelope_id = $1 ORDER BY signing_order, id LIMIT 1`
	args := []any{envelopeID}
	if recipientID > 0 {
		q = `SELECT id, name FROM sign_recipients WHERE envelope_id = $1 AND id = $2`
		args = []any{envelopeID, recipientID}
	}
	var rid int64
	if err := s.pg.QueryRowContext(ctx, q, args...).Scan(&rid, &v.RecipientName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrBadInput
		}
		return nil, err
	}
	_ = s.pg.QueryRowContext(ctx,
		`SELECT email FROM sign_recipients WHERE id = $1`, rid).Scan(&v.RecipientMail)
	_ = s.pg.QueryRowContext(ctx,
		`SELECT count(*) FROM sign_recipients WHERE envelope_id = $1`, envelopeID).Scan(&v.TotalSigners)
	v.EnvelopeState = "sent" // so the preview is fully interactive
	v.Status = "viewed"
	rows, err := s.pg.QueryContext(ctx, `
		SELECT id, recipient_id, kind, page, x, y, w, h, required, meta, COALESCE(value, '')
		  FROM sign_fields WHERE envelope_id = $1 AND recipient_id = $2 ORDER BY page, id`,
		envelopeID, rid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	v.Fields = []*Field{}
	for rows.Next() {
		var f Field
		if err := rows.Scan(&f.ID, &f.RecipientID, &f.Kind, &f.Page, &f.X, &f.Y, &f.W, &f.H, &f.Required, &f.Meta, &f.Value); err != nil {
			return nil, err
		}
		v.Fields = append(v.Fields, &f)
	}
	return &v, nil
}

// CeremonyPage renders a page for the signer (token-scoped).
func (s *Service) CeremonyPage(ctx context.Context, token string, page int) ([]byte, error) {
	c, err := s.ceremonyRow(ctx, token)
	if err != nil {
		return nil, err
	}
	return s.RenderPage(ctx, c.tenantID, c.envelopeID, page)
}

// CompleteInput is the signer's final submission.
type CompleteInput struct {
	Consent       bool             `json:"consent"`
	SignatureKind string           `json:"signature_kind"` // drawn | typed
	SignaturePNG  string           `json:"signature_png"`  // data URL
	InitialsPNG   string           `json:"initials_png"`   // data URL (two-letter script)
	Values        map[int64]string `json:"-"`
	RawValues     []struct {
		ID    int64  `json:"id"`
		Value string `json:"value"`
	} `json:"fields"`
}

const maxSignaturePNG = 300 * 1024 // decoded bytes

// CompleteCeremony finishes a recipient's signing: stores the adopted
// signature as an encrypted blob, records consent + field values, marks the
// recipient signed, and — when every recipient has signed — completes the
// envelope and notifies the sender.
func (s *Service) CompleteCeremony(ctx context.Context, token string, signerIP, signerUA string, in CompleteInput) (*CeremonyView, error) {
	c, err := s.ceremonyRow(ctx, token)
	if err != nil {
		return nil, err
	}
	if c.envStatus != "sent" {
		if c.recStatus == "signed" {
			return nil, ErrAlreadySigned
		}
		return nil, ErrNotSignable
	}
	if c.recStatus == "signed" {
		return nil, ErrAlreadySigned
	}
	if !in.Consent {
		return nil, ErrNoConsent
	}

	// This recipient's fields — every required one needs a value.
	fields, err := s.fields(ctx, c.envelopeID)
	if err != nil {
		return nil, err
	}
	in.Values = map[int64]string{}
	for _, rv := range in.RawValues {
		in.Values[rv.ID] = strings.TrimSpace(rv.Value)
	}
	var recName, recEmail string
	_ = s.pg.QueryRowContext(ctx,
		`SELECT name, email FROM sign_recipients WHERE id = $1`, c.recipientID).Scan(&recName, &recEmail)
	needsSignature := false
	for _, f := range fields {
		if f.RecipientID != c.recipientID {
			continue
		}
		switch f.Kind {
		case "signature", "initials":
			needsSignature = true
			if in.Values[f.ID] == "" {
				in.Values[f.ID] = "signed"
			}
			continue
		case "date", "date_signed":
			// auto-stamped with the signing date, always
			in.Values[f.ID] = time.Now().UTC().Format("01/02/2006")
			continue
		case "name":
			if in.Values[f.ID] == "" {
				in.Values[f.ID] = recName
			}
		case "email":
			if in.Values[f.ID] == "" {
				in.Values[f.ID] = recEmail
			}
		case "number", "formula":
			if v := in.Values[f.ID]; v != "" {
				if _, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64); err != nil {
					return nil, ErrMissingFields
				}
			}
		case "dropdown":
			// the submitted value must be one of the sender's options
			if v := in.Values[f.ID]; v != "" {
				var m struct {
					Options []string `json:"options"`
				}
				_ = json.Unmarshal(f.Meta, &m)
				ok := len(m.Options) == 0
				for _, o := range m.Options {
					if o == v {
						ok = true
						break
					}
				}
				if !ok {
					return nil, ErrMissingFields
				}
			}
		case "radio", "checkbox":
			if v := in.Values[f.ID]; v != "" && v != "true" {
				return nil, ErrMissingFields
			}
		case "approve":
			if v := in.Values[f.ID]; v != "" && v != "approved" {
				return nil, ErrMissingFields
			}
		case "attachment":
			// required attachments must have been uploaded beforehand; the value
			// (filename) was set at upload time — the payload must not clobber it
			delete(in.Values, f.ID)
			if f.Required {
				var blob sql.NullString
				_ = s.pg.QueryRowContext(ctx,
					`SELECT value_blob FROM sign_fields WHERE id = $1`, f.ID).Scan(&blob)
				if !blob.Valid || blob.String == "" {
					return nil, ErrMissingFields
				}
			}
			continue
		case "decline":
			delete(in.Values, f.ID)
			continue // acted on via the decline endpoint, never a value
		}
		// required radio groups: satisfied if ANY radio in the group is picked
		if f.Kind == "radio" && f.Required && in.Values[f.ID] == "" {
			if radioGroupSatisfied(fields, in.Values, f, c.recipientID) {
				continue
			}
			return nil, ErrMissingFields
		}
		if f.Required && in.Values[f.ID] == "" && f.Kind != "checkbox" {
			return nil, ErrMissingFields
		}
	}

	// Adopted signature image (required whenever signature/initials fields exist).
	var sigBlob string
	if needsSignature {
		kind := in.SignatureKind
		if kind != "drawn" && kind != "typed" {
			return nil, ErrBadSignature
		}
		png := in.SignaturePNG
		if i := strings.Index(png, ","); i >= 0 && strings.HasPrefix(png, "data:image/png") {
			png = png[i+1:]
		} else {
			return nil, ErrBadSignature
		}
		raw, err := base64.StdEncoding.DecodeString(png)
		if err != nil || len(raw) == 0 || len(raw) > maxSignaturePNG {
			return nil, ErrBadSignature
		}
		put, err := s.store.Put(ctx, c.tenantID, bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("sign: store signature: %w", err)
		}
		sigBlob = put.BlobPath
		if _, err := s.pg.ExecContext(ctx, `
			UPDATE sign_recipients SET signature_blob = $1, signature_kind = $2 WHERE id = $3`,
			sigBlob, kind, c.recipientID); err != nil {
			return nil, err
		}
		// initials image (optional; falls back to the full signature on stamp)
		if ini := in.InitialsPNG; ini != "" {
			if i := strings.Index(ini, ","); i >= 0 && strings.HasPrefix(ini, "data:image/png") {
				if raw, err := base64.StdEncoding.DecodeString(ini[i+1:]); err == nil && len(raw) > 0 && len(raw) <= maxSignaturePNG {
					if put, err := s.store.Put(ctx, c.tenantID, bytes.NewReader(raw)); err == nil {
						_, _ = s.pg.ExecContext(ctx, `
							UPDATE sign_recipients SET signature_initials_blob = $1 WHERE id = $2`,
							put.BlobPath, c.recipientID)
					}
				}
			}
		}
	}

	// Persist field values (only this recipient's fields).
	for id, val := range in.Values {
		if _, err := s.pg.ExecContext(ctx, `
			UPDATE sign_fields SET value = $1
			 WHERE id = $2 AND envelope_id = $3 AND recipient_id = $4`,
			val, id, c.envelopeID, c.recipientID); err != nil {
			return nil, err
		}
	}

	// Full session metadata for the Certificate of Completion: IP, GeoIP
	// (city/region/country + coordinates via system/qserve), and user agent.
	var city, region, country string
	var lat, lon sql.NullFloat64
	if s.geo != nil && signerIP != "" {
		if g, err := s.geo.LookupIP(signerIP); err == nil && g != nil {
			city, country = g.City, g.CountryName
			if len(g.Subdivisions) > 0 {
				region = g.Subdivisions[0].Name
			}
			if g.Latitude != 0 || g.Longitude != 0 {
				lat = sql.NullFloat64{Float64: g.Latitude, Valid: true}
				lon = sql.NullFloat64{Float64: g.Longitude, Valid: true}
			}
		}
	}
	if _, err := s.pg.ExecContext(ctx, `
		UPDATE sign_recipients
		   SET status = 'signed', signed_at = now(), consent_at = now(), signer_ip = $2,
		       signer_ua = $3, signer_city = $4, signer_region = $5, signer_country = $6,
		       signer_lat = $7, signer_lon = $8
		 WHERE id = $1`,
		c.recipientID, nullStr(signerIP), signerUA, city, region, country, lat, lon); err != nil {
		return nil, err
	}
	s.event(ctx, c.envelopeID, "signer", "consented", recName)
	s.event(ctx, c.envelopeID, "signer", "signed", recName)

	// All signed? Complete the envelope + notify the sender.
	var remaining int
	if err := s.pg.QueryRowContext(ctx, `
		SELECT count(*) FROM sign_recipients WHERE envelope_id = $1 AND status <> 'signed'`,
		c.envelopeID).Scan(&remaining); err == nil && remaining == 0 {
		if _, err := s.pg.ExecContext(ctx, `
			UPDATE sign_envelopes SET status = 'completed', completed_at = now(), updated_at = now()
			 WHERE id = $1 AND status = 'sent'`, c.envelopeID); err == nil {
			s.event(ctx, c.envelopeID, "system", "completed", "")
			// Flatten + seal the executed document (best-effort; the envelope is
			// already completed and download falls back to the flat doc).
			if err := s.Execute(ctx, c.envelopeID); err != nil {
				logging.Error(packageName, "execute envelope %d: %v", c.envelopeID, err)
			}
			s.notifySenderCompleted(ctx, c.envelopeID)
			s.notifySignersCompleted(ctx, c.envelopeID)
		}
	}

	return s.CeremonyViewFull(ctx, token)
}

func radioGroup(f *Field) string {
	var m struct {
		Group string `json:"group"`
	}
	_ = json.Unmarshal(f.Meta, &m)
	if m.Group == "" {
		return "default"
	}
	return m.Group
}

// radioGroupSatisfied reports whether any radio in f's group carries a value.
func radioGroupSatisfied(fields []*Field, values map[int64]string, f *Field, recipientID int64) bool {
	g := radioGroup(f)
	for _, o := range fields {
		if o.Kind == "radio" && o.RecipientID == recipientID && radioGroup(o) == g && values[o.ID] == "true" {
			return true
		}
	}
	return false
}

const maxAttachment = 10 << 20 // 10 MiB

// AttachFile stores a signer-uploaded file for an attachment field (encrypted,
// like everything else). Token-scoped; only while the envelope is signable.
func (s *Service) AttachFile(ctx context.Context, token string, fieldID int64, filename string, r io.Reader) error {
	c, err := s.ceremonyRow(ctx, token)
	if err != nil {
		return err
	}
	if c.envStatus != "sent" || c.recStatus == "signed" {
		return ErrNotSignable
	}
	var kind string
	err = s.pg.QueryRowContext(ctx, `
		SELECT kind FROM sign_fields
		 WHERE id = $1 AND envelope_id = $2 AND recipient_id = $3`,
		fieldID, c.envelopeID, c.recipientID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != "attachment") {
		return ErrBadInput
	}
	if err != nil {
		return err
	}
	put, err := s.store.Put(ctx, c.tenantID, io.LimitReader(r, maxAttachment+1))
	if err != nil {
		return fmt.Errorf("sign: store attachment: %w", err)
	}
	if put.Size > maxAttachment {
		return ErrBadInput
	}
	filename = strings.TrimSpace(filename)
	if len(filename) > 140 {
		filename = filename[:140]
	}
	if filename == "" {
		filename = "attachment"
	}
	if _, err := s.pg.ExecContext(ctx, `
		UPDATE sign_fields SET value = $1, value_blob = $2 WHERE id = $3`,
		filename, put.BlobPath, fieldID); err != nil {
		return err
	}
	var recName string
	_ = s.pg.QueryRowContext(ctx, `SELECT name FROM sign_recipients WHERE id = $1`, c.recipientID).Scan(&recName)
	s.event(ctx, c.envelopeID, "signer", "attachment_uploaded", fmt.Sprintf("%s (%s)", filename, recName))
	return nil
}

// DeclineCeremony is the signer's terminal "no": the recipient is marked
// declined, the envelope closes as declined, and the sender is notified.
func (s *Service) DeclineCeremony(ctx context.Context, token, signerIP, reason string) error {
	c, err := s.ceremonyRow(ctx, token)
	if err != nil {
		return err
	}
	if c.envStatus != "sent" {
		return ErrNotSignable
	}
	if c.recStatus == "signed" {
		return ErrAlreadySigned
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		reason = reason[:500]
	}
	if _, err := s.pg.ExecContext(ctx, `
		UPDATE sign_recipients SET status = 'declined', signer_ip = $2 WHERE id = $1`,
		c.recipientID, nullStr(signerIP)); err != nil {
		return err
	}
	if _, err := s.pg.ExecContext(ctx, `
		UPDATE sign_envelopes SET status = 'declined', updated_at = now()
		 WHERE id = $1 AND status = 'sent'`, c.envelopeID); err != nil {
		return err
	}
	var recName string
	_ = s.pg.QueryRowContext(ctx, `SELECT name FROM sign_recipients WHERE id = $1`, c.recipientID).Scan(&recName)
	detail := recName
	if reason != "" {
		detail += ": " + reason
	}
	s.event(ctx, c.envelopeID, "signer", "declined", detail)
	s.notifySenderDeclined(ctx, c.envelopeID, recName, reason)
	return nil
}

func (s *Service) notifySenderDeclined(ctx context.Context, envelopeID int64, signer, reason string) {
	var title, senderEmail string
	var senderName sql.NullString
	if err := s.pg.QueryRowContext(ctx, `
		SELECT e.title, u.email, u.full_name
		  FROM sign_envelopes e JOIN users u ON u.id = e.created_by_user_id
		 WHERE e.id = $1`, envelopeID,
	).Scan(&title, &senderEmail, &senderName); err != nil {
		logging.Error(packageName, "declined-notify lookup: %v", err)
		return
	}
	in := mailer.SignDeclinedEmail{
		To: senderEmail, Name: senderName.String, Title: title, Signer: signer, Reason: reason,
	}
	if err := s.mail.SendSignDeclined(ctx, in); err != nil {
		logging.Error(packageName, "declined-notify send: %v", err)
	}
}

// notifySignersCompleted mails each signer that the document is fully signed,
// with their ceremony link (the terminal page offers the sealed copy download).
func (s *Service) notifySignersCompleted(ctx context.Context, envelopeID int64) {
	var title string
	if err := s.pg.QueryRowContext(ctx,
		`SELECT title FROM sign_envelopes WHERE id = $1`, envelopeID).Scan(&title); err != nil {
		return
	}
	rows, err := s.pg.QueryContext(ctx,
		`SELECT name, email, token FROM sign_recipients WHERE envelope_id = $1 ORDER BY signing_order, id`, envelopeID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var name, email, token string
		if rows.Scan(&name, &email, &token) != nil {
			continue
		}
		in := mailer.SignFinalCopyEmail{
			To: email, Name: name, Title: title,
			Link: "https://app.trilli.com/sign/" + token,
		}
		if err := s.mail.SendSignFinalCopy(ctx, in); err != nil {
			logging.Error(packageName, "final-copy notice to %s: %v", email, err)
		}
	}
}

func (s *Service) notifySenderCompleted(ctx context.Context, envelopeID int64) {
	var title, senderEmail string
	var senderName sql.NullString
	err := s.pg.QueryRowContext(ctx, `
		SELECT e.title, u.email, u.full_name
		  FROM sign_envelopes e JOIN users u ON u.id = e.created_by_user_id
		 WHERE e.id = $1`, envelopeID,
	).Scan(&title, &senderEmail, &senderName)
	if err != nil {
		logging.Error(packageName, "completed-notify lookup: %v", err)
		return
	}
	var signers []string
	rows, err := s.pg.QueryContext(ctx,
		`SELECT name FROM sign_recipients WHERE envelope_id = $1 ORDER BY signing_order, id`, envelopeID)
	if err == nil {
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				signers = append(signers, n)
			}
		}
		rows.Close()
	}
	in := mailer.SignCompletedEmail{
		To: senderEmail, Name: senderName.String, Title: title,
		Signers: strings.Join(signers, ", "),
	}
	if err := s.mail.SendSignCompleted(ctx, in); err != nil {
		logging.Error(packageName, "completed-notify send: %v", err)
	}
}
