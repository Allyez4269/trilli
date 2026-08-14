package sign

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"math/big"
	"strings"
	"time"

	pdfreader "github.com/digitorus/pdf"
	pdfsign "github.com/digitorus/pdfsign/sign"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"trilli/system/files"
	"trilli/system/logging"
	"trilli/system/pdftools"
)

// The executed document. On completion the immutable snapshot is flattened —
// every signature image and field value composited onto high-DPI page rasters —
// then the flat PDF is sealed with a PKCS#7 digital signature (digitorus/
// pdfsign, BSD-2). Rasterizing guarantees the output is EXACTLY what the signer
// saw, and the seal makes any post-signing alteration cryptographically
// detectable. Both artifacts are envelope-owned encrypted blobs.

const executeDPI = 150

var regularFace = mustFace(goregular.TTF)

func mustFace(ttf []byte) *opentype.Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return f
}

// Execute flattens + seals a completed envelope and stores both blobs. Runs
// once (idempotent: skips if already executed). Best-effort from the caller's
// side — the envelope is already completed; a stamping hiccup is logged and can
// be retried, and download falls back to the flat executed doc if unsealed.
func (s *Service) Execute(ctx context.Context, envelopeID int64) error {
	var tenantID int64
	var blobPath, executed sql.NullString
	err := s.pg.QueryRowContext(ctx, `
		SELECT tenant_id, blob_path, executed_blob FROM sign_envelopes WHERE id = $1`, envelopeID,
	).Scan(&tenantID, &blobPath, &executed)
	if err != nil {
		return err
	}
	if executed.Valid && executed.String != "" {
		return nil // already executed
	}

	raw, err := s.readBlob(ctx, blobPath.String)
	if err != nil {
		return err
	}
	pages, err := pdftools.RenderPages(raw, executeDPI)
	if err != nil {
		return fmt.Errorf("sign: render for execute: %w", err)
	}

	fields, err := s.fields(ctx, envelopeID)
	if err != nil {
		return err
	}
	sigByRecipient, err := s.signatureImages(ctx, envelopeID, tenantID)
	if err != nil {
		return err
	}
	iniByRecipient, err := s.initialsImages(ctx, envelopeID)
	if err != nil {
		return err
	}

	// composite fields onto each page raster
	stamped := make([]io.Reader, len(pages))
	for i, pg := range pages {
		rgba := toRGBA(pg)
		pageNo := i + 1
		for _, f := range fields {
			if f.Page != pageNo {
				continue
			}
			ink := sigByRecipient[f.RecipientID]
			if f.Kind == "initials" {
				if ini := iniByRecipient[f.RecipientID]; ini != nil {
					ink = ini
				}
			}
			drawField(rgba, f, ink, s.fieldValue(ctx, f.ID))
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, rgba); err != nil {
			return err
		}
		stamped[i] = &buf
	}

	// Append the Certificate of Completion as the final page (so it lives inside
	// the same seal). Best-effort: a cert hiccup must not block the executed doc.
	if certImg, cerr := s.certificateImage(ctx, envelopeID); cerr == nil {
		var cbuf bytes.Buffer
		if png.Encode(&cbuf, certImg) == nil {
			stamped = append(stamped, &cbuf)
		}
	} else {
		logging.Error(packageName, "certificate for envelope %d: %v", envelopeID, cerr)
	}

	var flat bytes.Buffer
	if err := pdftools.ImageToPDF(stamped, &flat); err != nil {
		return fmt.Errorf("sign: rebuild executed pdf: %w", err)
	}
	execPut, err := s.store.Put(ctx, tenantID, bytes.NewReader(flat.Bytes()))
	if err != nil {
		return fmt.Errorf("sign: store executed: %w", err)
	}
	execDigest := fmt.Sprintf("%x", sha256.Sum256(flat.Bytes()))
	if _, err := s.pg.ExecContext(ctx,
		`UPDATE sign_envelopes SET executed_blob = $1, executed_sha256 = $2 WHERE id = $3`,
		execPut.BlobPath, execDigest, envelopeID); err != nil {
		return err
	}

	// cryptographic seal (best-effort layered on top of the visible executed doc)
	sealed, err := s.seal(ctx, flat.Bytes(), envelopeID)
	if err != nil {
		logging.Error(packageName, "seal envelope %d: %v", envelopeID, err)
		s.event(ctx, envelopeID, "system", "executed", "flattened (seal deferred)")
		return nil
	}
	sealPut, err := s.store.Put(ctx, tenantID, bytes.NewReader(sealed))
	if err != nil {
		return err
	}
	// the preview now renders the executed document — drop cached template pages
	s.dropPageCache(envelopeID)
	sealDigest := fmt.Sprintf("%x", sha256.Sum256(sealed))
	if _, err := s.pg.ExecContext(ctx,
		`UPDATE sign_envelopes SET sealed_blob = $1, sealed_sha256 = $2 WHERE id = $3`,
		sealPut.BlobPath, sealDigest, envelopeID); err != nil {
		return err
	}
	// The digest lands in the append-only trail: an independently timestamped
	// record of EXACTLY which bytes were sealed.
	s.event(ctx, envelopeID, "system", "sealed", "PKCS#7 digital signature applied · sha256:"+sealDigest)
	s.depositCompleted(ctx, envelopeID, tenantID, sealed)
	return nil
}

// depositCompleted files the finished agreement — the executed, sealed PDF —
// into the tenant's configured "Envelopes save to" destination (default: the
// account-default workspace root). One artifact, named "<title> (signed).pdf";
// the working template stays inside Trilli Sign. Best-effort: a Files hiccup
// never blocks completion (download always serves the sealed blob).
func (s *Service) depositCompleted(ctx context.Context, envelopeID, tenantID int64, pdf []byte) {
	if s.deposit == nil {
		return
	}
	var title string
	var creator int64
	if err := s.pg.QueryRowContext(ctx,
		`SELECT title, created_by_user_id FROM sign_envelopes WHERE id = $1`, envelopeID,
	).Scan(&title, &creator); err != nil {
		return
	}
	var ws, fo sql.NullInt64
	_ = s.pg.QueryRowContext(ctx,
		`SELECT workspace_id, folder_id FROM sign_settings WHERE tenant_id = $1`, tenantID,
	).Scan(&ws, &fo)
	if !fo.Valid {
		// no explicit destination — the system "Trilli Sign/Signed Agreements"
		if _, signedID, err := s.ensureSignFolders(ctx, tenantID, creator); err == nil {
			fo = sql.NullInt64{Int64: signedID, Valid: true}
			ws = sql.NullInt64{}
		}
	}
	in := files.UploadInput{
		TenantID:        tenantID,
		UploaderID:      creator,
		Name:            title + " (signed).pdf",
		ContentType:     "application/pdf",
		Reader:          bytes.NewReader(pdf),
		ProtectedSource: "trilli-sign", // undeletable from Files; removed with the envelope
	}
	if fo.Valid {
		in.ParentFolderID = &fo.Int64
	}
	if ws.Valid {
		in.WorkspaceID = &ws.Int64
	}
	f, err := s.deposit.Upload(ctx, in)
	if err != nil && fo.Valid {
		// configured folder may be gone — retry at the workspace root
		in.ParentFolderID = nil
		f, err = s.deposit.Upload(ctx, in)
	}
	if err != nil {
		logging.Error(packageName, "deposit signed copy for envelope %d: %v", envelopeID, err)
		return
	}
	_, _ = s.pg.ExecContext(ctx,
		`UPDATE sign_envelopes SET deposited_file_id = $1 WHERE id = $2`, f.ID, envelopeID)
	s.event(ctx, envelopeID, "system", "filed", fmt.Sprintf("saved to Files as %q", f.Name))
}

func (s *Service) readBlob(ctx context.Context, path string) ([]byte, error) {
	rc, err := s.store.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (s *Service) fieldValue(ctx context.Context, fieldID int64) string {
	var v string
	_ = s.pg.QueryRowContext(ctx, `SELECT value FROM sign_fields WHERE id = $1`, fieldID).Scan(&v)
	return v
}

// signatureImages loads each recipient's adopted signature PNG (decoded).
func (s *Service) signatureImages(ctx context.Context, envelopeID, tenantID int64) (map[int64]image.Image, error) {
	rows, err := s.pg.QueryContext(ctx,
		`SELECT id, signature_blob FROM sign_recipients WHERE envelope_id = $1`, envelopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]image.Image{}
	type rec struct {
		id   int64
		blob string
	}
	var recs []rec
	for rows.Next() {
		var id int64
		var blob sql.NullString
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		if blob.Valid && blob.String != "" {
			recs = append(recs, rec{id, blob.String})
		}
	}
	for _, r := range recs {
		raw, err := s.readBlob(ctx, r.blob)
		if err != nil {
			continue
		}
		if img, err := png.Decode(bytes.NewReader(raw)); err == nil {
			out[r.id] = img
		}
	}
	return out, nil
}

// initialsImages loads each recipient's adopted initials PNG (decoded).
func (s *Service) initialsImages(ctx context.Context, envelopeID int64) (map[int64]image.Image, error) {
	rows, err := s.pg.QueryContext(ctx,
		`SELECT id, signature_initials_blob FROM sign_recipients WHERE envelope_id = $1`, envelopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]image.Image{}
	type rec struct {
		id   int64
		blob string
	}
	var recs []rec
	for rows.Next() {
		var id int64
		var blob string
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		if blob != "" {
			recs = append(recs, rec{id, blob})
		}
	}
	for _, r := range recs {
		raw, err := s.readBlob(ctx, r.blob)
		if err != nil {
			continue
		}
		if img, err := png.Decode(bytes.NewReader(raw)); err == nil {
			out[r.id] = img
		}
	}
	return out, nil
}

func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, src, b.Min, draw.Src)
	return dst
}

// drawField composites one field onto the page raster at its normalized box.
func drawField(page *image.RGBA, f *Field, sig image.Image, value string) {
	b := page.Bounds()
	pw, ph := float64(b.Dx()), float64(b.Dy())
	x0 := int(f.X * pw)
	y0 := int(f.Y * ph)
	fw := int(f.W * pw)
	fh := int(f.H * ph)
	box := image.Rect(b.Min.X+x0, b.Min.Y+y0, b.Min.X+x0+fw, b.Min.Y+y0+fh)

	switch f.Kind {
	case "signature", "initials":
		if sig == nil {
			return
		}
		// anchor the ink to the BOTTOM of the box so it sits on the line
		drawImageFitBottom(page, box, sig)
	case "checkbox":
		if value == "true" {
			drawCheck(page, box)
		}
	case "radio":
		drawRadio(page, box, value == "true")
	case "approve":
		if value == "approved" {
			drawText(page, box, "Approved")
		}
	case "attachment":
		if value != "" {
			drawText(page, box, "[Attached] "+value)
		}
	case "note":
		drawMultilineText(page, box, value)
	case "decline":
		// never stamped — a decline closes the envelope before execution
	default: // date, text, number, dropdown, formula, name, email, company, title
		if value != "" {
			drawText(page, box, value)
		}
	}
}

// drawRadio renders a radio cell: an outlined circle, filled when selected.
func drawRadio(dst *image.RGBA, box image.Rectangle, selected bool) {
	cx := float64(box.Min.X+box.Max.X) / 2
	cy := float64(box.Min.Y+box.Max.Y) / 2
	r := float64(min(box.Dx(), box.Dy())) / 2.2
	col := image.Black
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			if d <= r && d >= r-2.2 { // ring
				dst.Set(x, y, col.At(0, 0))
			}
			if selected && d <= r-4.5 { // filled core
				dst.Set(x, y, col.At(0, 0))
			}
		}
	}
}

// drawMultilineText lays out newline-separated text top-down inside the box.
func drawMultilineText(dst *image.RGBA, box image.Rectangle, value string) {
	if value == "" {
		return
	}
	lines := strings.Split(value, "\n")
	if len(lines) == 0 {
		return
	}
	lineH := box.Dy() / len(lines)
	if lineH < 12 {
		lineH = 12
	}
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		top := box.Min.Y + i*lineH
		if top+lineH > box.Max.Y+lineH { // allow slight overflow of the last line
			break
		}
		drawText(dst, image.Rect(box.Min.X, top, box.Max.X, top+lineH), ln)
	}
}

// drawImageFitBottom scales img to fit the box (preserving aspect), centered
// horizontally but anchored to the bottom edge — signatures land on the line.
func drawImageFitBottom(dst *image.RGBA, box image.Rectangle, img image.Image) {
	ib := img.Bounds()
	iw, ih := float64(ib.Dx()), float64(ib.Dy())
	if iw == 0 || ih == 0 {
		return
	}
	// signatures may stand ~45% taller than the line box (like real ink) —
	// matches the ceremony overlay so what you see is what gets sealed
	scale := float64(box.Dx()) / iw
	if 1.45*float64(box.Dy())/ih < scale {
		scale = 1.45 * float64(box.Dy()) / ih
	}
	tw, th := int(iw*scale), int(ih*scale)
	ox := box.Min.X + (box.Dx()-tw)/2
	// nudge below the box bottom so the BASELINE lands on the document's line
	// and descenders cross it naturally, like a real pen signature
	oy := box.Max.Y - th + int(0.13*float64(th))
	for y := 0; y < th; y++ {
		sy := ib.Min.Y + int(float64(y)/scale)
		for x := 0; x < tw; x++ {
			sx := ib.Min.X + int(float64(x)/scale)
			c := img.At(sx, sy)
			if _, _, _, a := c.RGBA(); a > 0x2000 {
				dst.Set(ox+x, oy+y, c)
			}
		}
	}
}

// drawImageFit scales img to fit within box (preserving aspect), centered.
func drawImageFit(dst *image.RGBA, box image.Rectangle, img image.Image) {
	ib := img.Bounds()
	iw, ih := float64(ib.Dx()), float64(ib.Dy())
	if iw == 0 || ih == 0 {
		return
	}
	scale := (float64(box.Dx()) / iw)
	if float64(box.Dy())/ih < scale {
		scale = float64(box.Dy()) / ih
	}
	tw, th := int(iw*scale), int(ih*scale)
	ox := box.Min.X + (box.Dx()-tw)/2
	oy := box.Min.Y + (box.Dy()-th)/2
	// nearest-neighbor scale (signatures are line art; keeps it crisp + dep-free)
	for y := 0; y < th; y++ {
		sy := ib.Min.Y + int(float64(y)/scale)
		for x := 0; x < tw; x++ {
			sx := ib.Min.X + int(float64(x)/scale)
			c := img.At(sx, sy)
			if _, _, _, a := c.RGBA(); a > 0x2000 {
				dst.Set(ox+x, oy+y, c)
			}
		}
	}
}

func drawText(dst *image.RGBA, box image.Rectangle, text string) {
	size := float64(box.Dy()) * 0.72
	if size < 8 {
		size = 8
	}
	face, err := opentype.NewFace(regularFace, &opentype.FaceOptions{Size: size, DPI: 72})
	if err != nil {
		return
	}
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(image.Black),
		Face: face,
	}
	// baseline near the box bottom so values align with the document's line
	// (small descender allowance)
	d.Dot = fixed.Point26_6{
		X: fixed.I(box.Min.X + 2),
		Y: fixed.I(box.Max.Y - int(size*0.18)),
	}
	d.DrawString(text)
}

func drawCheck(dst *image.RGBA, box image.Rectangle) {
	// simple two-stroke check inside the box
	col := image.Black
	line := func(x0, y0, x1, y1 int) {
		steps := abs(x1-x0) + abs(y1-y0)
		if steps == 0 {
			return
		}
		for i := 0; i <= steps; i++ {
			t := float64(i) / float64(steps)
			x := x0 + int(float64(x1-x0)*t)
			y := y0 + int(float64(y1-y0)*t)
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					dst.Set(x+dx, y+dy, col.At(0, 0))
				}
			}
		}
	}
	w, h := box.Dx(), box.Dy()
	line(box.Min.X+w/6, box.Min.Y+h/2, box.Min.X+w*2/5, box.Min.Y+h*3/4)
	line(box.Min.X+w*2/5, box.Min.Y+h*3/4, box.Min.X+w*5/6, box.Min.Y+h/5)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ----- cryptographic seal (PKCS#7 via digitorus/pdfsign) ----------------------

const signCertProvider = "trilli_sign"

// seal applies an approval-type digital signature to the flattened PDF using
// Trilli Sign's issuing certificate.
func (s *Service) seal(ctx context.Context, flat []byte, envelopeID int64) ([]byte, error) {
	signer, cert, err := s.ensureSigner(ctx)
	if err != nil {
		return nil, err
	}
	rdr, err := pdfreader.NewReader(bytes.NewReader(flat), int64(len(flat)))
	if err != nil {
		return nil, fmt.Errorf("pdf reader: %w", err)
	}
	var out bytes.Buffer
	err = pdfsign.Sign(bytes.NewReader(flat), &out, rdr, int64(len(flat)), pdfsign.SignData{
		Signature: pdfsign.SignDataSignature{
			CertType: pdfsign.ApprovalSignature,
			Info: pdfsign.SignDataSignatureInfo{
				Name:        "Trilli Sign",
				Location:    "Trilli",
				Reason:      fmt.Sprintf("Executed via Trilli Sign (envelope %d)", envelopeID),
				ContactInfo: "trilli.com",
				Date:        time.Now(),
			},
		},
		Signer:          signer,
		Certificate:     cert,
		DigestAlgorithm: crypto.SHA256,
	})
	if err != nil {
		return nil, fmt.Errorf("pkcs7 sign: %w", err)
	}
	return out.Bytes(), nil
}

// ensureSigner loads Trilli Sign's RSA issuing cert + key from the credentials
// vault, generating and persisting a self-signed one on first use. The seal is
// tamper-evidence (was this altered after signing?), not third-party identity —
// a self-signed org cert is the correct trust anchor here, the same model as
// self-hosted DocuSeal/Documenso.
func (s *Service) ensureSigner(ctx context.Context) (crypto.Signer, *x509.Certificate, error) {
	certPEM, _, err := s.creds.GetActive(ctx, signCertProvider, "cert")
	keyPEM, _, err2 := s.creds.GetActive(ctx, signCertProvider, "key")
	if err == nil && err2 == nil && certPEM != "" && keyPEM != "" {
		return parseSigner(certPEM, keyPEM)
	}
	// generate + persist
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   "Trilli Sign Issuing CA",
			Organization: []string{"Trilli Media LLC"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(25, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	kPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if err := s.creds.Set(ctx, signCertProvider, "cert", "live", cPEM); err != nil {
		return nil, nil, err
	}
	if err := s.creds.Set(ctx, signCertProvider, "key", "live", kPEM); err != nil {
		return nil, nil, err
	}
	logging.Info(packageName, "generated Trilli Sign issuing certificate")
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}

func parseSigner(certPEM, keyPEM string) (crypto.Signer, *x509.Certificate, error) {
	cb, _ := pem.Decode([]byte(certPEM))
	kb, _ := pem.Decode([]byte(keyPEM))
	if cb == nil || kb == nil {
		return nil, nil, errors.New("sign: bad stored certificate")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.ParsePKCS1PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}

// DownloadExecuted returns the best available executed document for an envelope
// (sealed if present, else the flattened executed doc), with its tenant id.
func (s *Service) DownloadExecuted(ctx context.Context, tenantID, id int64) ([]byte, string, error) {
	var sealed, executed sql.NullString
	var title string
	err := s.pg.QueryRowContext(ctx,
		`SELECT sealed_blob, executed_blob, title FROM sign_envelopes WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(&sealed, &executed, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	path := ""
	if sealed.Valid && sealed.String != "" {
		path = sealed.String
	} else if executed.Valid && executed.String != "" {
		path = executed.String
	} else {
		return nil, "", ErrNotFound
	}
	raw, err := s.readBlob(ctx, path)
	if err != nil {
		return nil, "", err
	}
	return raw, strings.TrimSpace(title) + " (signed).pdf", nil
}

// DownloadExecutedByToken lets a signer download the completed document from
// their ceremony link (token is the access).
func (s *Service) DownloadExecutedByToken(ctx context.Context, token string) ([]byte, string, error) {
	c, err := s.ceremonyRow(ctx, token)
	if err != nil {
		return nil, "", err
	}
	if c.envStatus != "completed" {
		return nil, "", ErrNotFound
	}
	return s.DownloadExecuted(ctx, c.tenantID, c.envelopeID)
}
