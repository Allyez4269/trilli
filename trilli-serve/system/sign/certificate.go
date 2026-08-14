package sign

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The Certificate of Completion: an audit page appended to the executed
// document (so it's inside the same PKCS#7 seal). It records the parties, their
// timestamps and IPs, and the full event trail — the tamper-evident story of
// how the document was executed.

var boldFace = mustFace(gobold.TTF)

const (
	certW = 1275 // Letter @ 150 DPI
	certH = 1650
)

var (
	inkColor   = color.RGBA{0x14, 0x21, 0x3b, 0xff}
	mutedColor = color.RGBA{0x5b, 0x6b, 0x82, 0xff}
	brandColor = color.RGBA{0x1d, 0x4e, 0xd8, 0xff}
	lineColor  = color.RGBA{0xe3, 0xe8, 0xf0, 0xff}
	navyColor  = color.RGBA{0x0a, 0x25, 0x40, 0xff}
)

type certData struct {
	title       string
	envelopeID  int64
	completedAt *time.Time
	sealed      bool
	recipients  []certParty
	events      []*Event
	// sealing-certificate identity — printed BEFORE sealing so any verifier
	// can bind the PDF's embedded PKCS#7 signer to this page's claim
	sealCertCN     string
	sealCertSerial string
	sealCertFP     string // SHA-256 fingerprint of the DER certificate
}

type certParty struct {
	name     string
	email    string
	signedAt *time.Time
	ip       string
	ua       string
	city     string
	region   string
	country  string
	lat, lon sql.NullFloat64
}

func (s *Service) certificateImage(ctx context.Context, envelopeID int64) (image.Image, error) {
	var d certData
	d.envelopeID = envelopeID
	var sealed sql.NullString
	if err := s.pg.QueryRowContext(ctx,
		`SELECT title, completed_at, sealed_blob FROM sign_envelopes WHERE id = $1`, envelopeID,
	).Scan(&d.title, &d.completedAt, &sealed); err != nil {
		return nil, err
	}
	d.sealed = sealed.Valid && sealed.String != ""

	rows, err := s.pg.QueryContext(ctx, `
		SELECT name, email, signed_at, signer_ip, signer_ua,
		       signer_city, signer_region, signer_country, signer_lat, signer_lon
		  FROM sign_recipients
		 WHERE envelope_id = $1 ORDER BY signing_order, id`, envelopeID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p certParty
		var ip sql.NullString
		if err := rows.Scan(&p.name, &p.email, &p.signedAt, &ip, &p.ua,
			&p.city, &p.region, &p.country, &p.lat, &p.lon); err != nil {
			rows.Close()
			return nil, err
		}
		p.ip = ip.String
		d.recipients = append(d.recipients, p)
	}
	rows.Close()

	if d.events, err = s.Events(ctx, tenantOf(ctx, s, envelopeID), envelopeID); err != nil {
		// events require tenant; fall back to a direct read
		d.events, _ = s.eventsRaw(ctx, envelopeID)
	}
	// identity of the certificate that will apply the seal (idempotent load)
	if _, cert, err := s.ensureSigner(ctx); err == nil && cert != nil {
		d.sealCertCN = cert.Subject.CommonName
		d.sealCertSerial = cert.SerialNumber.String()
		fp := sha256.Sum256(cert.Raw)
		d.sealCertFP = strings.ToUpper(hex.EncodeToString(fp[:]))
	}
	return drawCertificate(d), nil
}

// eventsRaw reads the trail without the tenant gate (internal use for the cert).
func (s *Service) eventsRaw(ctx context.Context, envelopeID int64) ([]*Event, error) {
	rows, err := s.pg.QueryContext(ctx, `
		SELECT actor, action, detail, created_at FROM sign_events
		 WHERE envelope_id = $1 ORDER BY created_at, id`, envelopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Event{}
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.Actor, &ev.Action, &ev.Detail, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &ev)
	}
	return out, nil
}

func tenantOf(ctx context.Context, s *Service, envelopeID int64) int64 {
	var t int64
	_ = s.pg.QueryRowContext(ctx, `SELECT tenant_id FROM sign_envelopes WHERE id = $1`, envelopeID).Scan(&t)
	return t
}

// --- drawing ---

type canvas struct {
	img *image.RGBA
}

func (c *canvas) text(x, y int, s string, size float64, f *opentype.Font, col color.Color) {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72})
	if err != nil {
		return
	}
	d := &font.Drawer{Dst: c.img, Src: image.NewUniform(col), Face: face,
		Dot: fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)}}
	d.DrawString(s)
}

func (c *canvas) hline(x0, x1, y int, col color.Color) {
	for x := x0; x < x1; x++ {
		c.img.Set(x, y, col)
	}
}

func (c *canvas) rect(r image.Rectangle, col color.Color) {
	draw.Draw(c.img, r, image.NewUniform(col), image.Point{}, draw.Src)
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.UTC().Format("Jan 2, 2006 · 15:04:05 MST")
}

func drawCertificate(d certData) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, certW, certH))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	c := &canvas{img}
	M := 90 // margin

	// header band
	c.rect(image.Rect(0, 0, certW, 150), navyColor)
	c.text(M, 68, "Trilli Sign", 34, boldFace, color.White)
	c.text(M, 110, "Certificate of Completion", 24, regularFace, color.RGBA{0xc7, 0xd6, 0xf0, 0xff})
	// tiny lock glyph area right
	c.text(certW-360, 90, "Encrypted · Auditable", 18, regularFace, color.RGBA{0x9d, 0xb4, 0xd8, 0xff})

	y := 235
	c.text(M, y, "Document", 15, boldFace, mutedColor)
	c.text(M, y+38, d.title, 28, boldFace, inkColor)
	c.text(M, y+72, fmt.Sprintf("Envelope #%d", d.envelopeID), 17, regularFace, mutedColor)
	right := "Status: COMPLETED"
	c.text(certW-M-260, y+38, right, 18, boldFace, color.RGBA{0x16, 0xa3, 0x4a, 0xff})
	c.text(certW-M-260, y+68, "Completed "+fmtTime(d.completedAt), 14, regularFace, mutedColor)
	certID := fmt.Sprintf("Certificate ID  TS-%06d", d.envelopeID)
	if d.completedAt != nil {
		certID += d.completedAt.UTC().Format("-20060102150405")
	}
	c.text(certW-M-260, y+92, certID, 13, regularFace, mutedColor)

	y += 120
	c.hline(M, certW-M, y, lineColor)

	// signers — each with the full signing-session record (IP, GeoIP location,
	// coordinates, and the complete user agent captured at signature time)
	y += 50
	c.text(M, y, "SIGNERS & SIGNING-SESSION METADATA", 15, boldFace, brandColor)
	y += 20
	orDash := func(v string) string {
		if v == "" {
			return "—"
		}
		return v
	}
	for i, p := range d.recipients {
		c.hline(M, certW-M, y, lineColor)
		y += 42
		c.text(M, y, fmt.Sprintf("%d.", i+1), 20, boldFace, inkColor)
		c.text(M+40, y, p.name, 22, boldFace, inkColor)
		c.text(M+40, y+28, p.email, 16, regularFace, mutedColor)
		c.text(certW-M-430, y, "Signed  "+fmtTime(p.signedAt), 16, boldFace, inkColor)
		y += 56

		// metadata grid: label column + value column, two per row
		loc := strings.TrimRight(strings.Join(nonEmpty(p.city, p.region, p.country), ", "), ", ")
		coords := "—"
		if p.lat.Valid || p.lon.Valid {
			coords = fmt.Sprintf("%.5f, %.5f", p.lat.Float64, p.lon.Float64)
		}
		meta := [][2]string{
			{"IP address", orDash(p.ip)},
			{"Location", orDash(loc)},
			{"Coordinates", coords},
		}
		for _, kv := range meta {
			c.text(M+40, y, strings.ToUpper(kv[0]), 12, boldFace, mutedColor)
			c.text(M+210, y, kv[1], 15, regularFace, inkColor)
			y += 26
		}
		// full user agent, wrapped (up to 2 lines)
		c.text(M+40, y, "USER AGENT", 12, boldFace, mutedColor)
		for j, line := range wrapText(orDash(p.ua), 88) {
			if j >= 2 {
				break
			}
			c.text(M+210, y, line, 13, regularFace, inkColor)
			y += 22
		}
		y += 14
	}
	c.hline(M, certW-M, y, lineColor)

	// event trail
	y += 55
	c.text(M, y, "AUDIT TRAIL", 15, boldFace, brandColor)
	y += 34
	for _, ev := range d.events {
		if y > certH-360 {
			break
		}
		ts := ev.CreatedAt.UTC().Format("Jan 2 15:04:05")
		c.text(M, y, ts, 15, regularFace, mutedColor)
		label := prettyAction(ev.Action)
		who := ev.Actor
		line := label
		if who != "" && who != "system" {
			line += "  ·  " + who
		}
		if ev.Detail != "" {
			line += "  (" + ev.Detail + ")"
		}
		c.text(M+220, y, line, 16, regularFace, inkColor)
		y += 34
	}

	// seal & verification — how a third party proves this document is intact.
	// (The seal itself is applied AFTER this page is rendered, so the page
	// carries the SIGNING CERTIFICATE's identity, not the signature bytes:
	// the embedded PKCS#7 must verify AND its signer must match this
	// fingerprint.)
	vy := certH - 320
	c.hline(M, certW-M, vy, lineColor)
	vy += 40
	c.text(M, vy, "SEAL & VERIFICATION", 15, boldFace, brandColor)
	vy += 30
	c.text(M, vy, "SEALING CERTIFICATE", 12, boldFace, mutedColor)
	cn := d.sealCertCN
	if cn == "" {
		cn = "—"
	} else if d.sealCertSerial != "" {
		cn += "  ·  serial " + d.sealCertSerial
	}
	c.text(M+240, vy, cn, 15, regularFace, inkColor)
	vy += 28
	c.text(M, vy, "SHA-256 FINGERPRINT", 12, boldFace, mutedColor)
	fp := d.sealCertFP
	if fp == "" {
		fp = "—"
	}
	// wrap the 64-hex fingerprint into two 32-char halves
	if len(fp) == 64 {
		c.text(M+240, vy, fp[:32], 15, regularFace, inkColor)
		vy += 24
		c.text(M+240, vy, fp[32:], 15, regularFace, inkColor)
	} else {
		c.text(M+240, vy, fp, 15, regularFace, inkColor)
	}
	vy += 30
	c.text(M, vy, "TO VERIFY", 12, boldFace, mutedColor)
	c.text(M+240, vy, "Open this PDF's signature panel (Adobe Acrobat) or run pdfsig: the signature must", 14, regularFace, inkColor)
	vy += 22
	c.text(M+240, vy, "validate as unmodified, and the signer certificate must match the fingerprint above.", 14, regularFace, inkColor)

	// seal footer
	fy := certH - 130
	c.rect(image.Rect(0, fy, certW, certH), color.RGBA{0xf5, 0xf8, 0xfc, 0xff})
	c.hline(0, certW, fy, lineColor)
	// The certificate is embedded in the executed document that Execute then
	// seals, so the finished artifact the reader holds is sealed.
	sealMsg := "Sealed with a PKCS#7 digital signature — any change after signing is cryptographically detectable."
	c.text(M, fy+52, sealMsg, 17, boldFace, inkColor)
	c.text(M, fy+84, "Trilli Sign · trilli.com · Encrypted, private, and auditable end to end.", 15, regularFace, mutedColor)
	return img
}

func prettyAction(a string) string {
	switch a {
	case "created":
		return "Envelope created"
	case "recipient_added":
		return "Recipient added"
	case "recipient_removed":
		return "Recipient removed"
	case "updated":
		return "Envelope updated"
	case "sent":
		return "Sent for signature"
	case "notified":
		return "Recipient notified"
	case "viewed":
		return "Document viewed"
	case "consented":
		return "Consent given"
	case "signed":
		return "Signed"
	case "completed":
		return "Completed"
	case "executed":
		return "Document executed"
	case "sealed":
		return "Cryptographic seal applied"
	case "filed":
		return "Signed copy saved to Files"
	case "voided":
		return "Voided"
	case "declined":
		return "Declined"
	case "attachment_uploaded":
		return "Attachment uploaded"
	default:
		return strings.Title(strings.ReplaceAll(a, "_", " "))
	}
}

// nonEmpty filters out empty strings, preserving order.
func nonEmpty(vals ...string) []string {
	out := []string{}
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// wrapText breaks s into lines of at most width characters on spaces (a long
// unbroken token is hard-split).
func wrapText(s string, width int) []string {
	if len(s) <= width {
		return []string{s}
	}
	var lines []string
	for len(s) > width {
		cut := strings.LastIndex(s[:width], " ")
		if cut < width/2 {
			cut = width
		}
		lines = append(lines, strings.TrimSpace(s[:cut]))
		s = strings.TrimSpace(s[cut:])
	}
	if s != "" {
		lines = append(lines, s)
	}
	return lines
}
