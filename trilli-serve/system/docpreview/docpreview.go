// Package docpreview renders Office documents (Word, Excel, PowerPoint, and
// their OpenDocument/RTF cousins) to PDF so they can be previewed in-app
// without shipping the original bytes to a third-party viewer.
//
// The design is deliberately two-layered so it can outgrow this process:
//
//   - Converter is the swappable engine. Today it's an in-process LibreOffice
//     (system/docpreview.LibreOfficeConverter). When demand scales, drop in a
//     remote Converter (e.g. a Gotenberg sidecar) — a one-line wiring change in
//     cmd/trilli; nothing else moves.
//   - Service wraps a Converter with a content-addressed cache (a
//     storage.KeyedStore) so a document is only ever converted once per
//     version. Cached PDFs live in a reserved, hidden prefix inside the
//     tenant's blob namespace and are pure derived data — never counted against
//     the tenant's quota, never a files row, safe to purge and rebuild.
package docpreview

import (
	"context"
	"io"
	"path/filepath"
	"strings"
)

const packageName = "docpreview"

// Converter renders a source document to PDF bytes. ext is the lowercase
// extension without a dot ("docx", "xlsx", …) — the engine needs it to pick
// the right import filter. Implementations must be safe for concurrent use.
type Converter interface {
	ToPDF(ctx context.Context, src io.Reader, ext string) ([]byte, error)
}

// convertibleExts is the set of source extensions we can render to PDF. Kept in
// sync with convertibleTypes below; either one matching is enough.
var convertibleExts = map[string]bool{
	// Word / text documents
	"doc": true, "docx": true, "odt": true, "rtf": true,
	// Spreadsheets
	"xls": true, "xlsx": true, "ods": true, "csv": true,
	// Presentations
	"ppt": true, "pptx": true, "odp": true,
}

// convertibleTypes maps MIME content types to true. Some uploads carry an
// accurate Content-Type but a generic name (or vice versa), so we accept a
// match on either axis.
var convertibleTypes = map[string]bool{
	"application/msword": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"application/vnd.oasis.opendocument.text":                                 true,
	"application/rtf": true,
	"text/rtf":        true,

	"application/vnd.ms-excel": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true,
	"application/vnd.oasis.opendocument.spreadsheet":                    true,

	"application/vnd.ms-powerpoint":                                             true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
	"application/vnd.oasis.opendocument.presentation":                           true,
}

// IsConvertible reports whether a file (by content type and/or name) is an
// Office document we can render to PDF for preview.
func IsConvertible(contentType, name string) bool {
	if ct := normalizeType(contentType); ct != "" && convertibleTypes[ct] {
		return true
	}
	return convertibleExts[ExtOf(name)]
}

// ExtOf returns the lowercase extension of name without the leading dot
// ("Report.DOCX" -> "docx"), or "" when there's no extension.
func ExtOf(name string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
}

// normalizeType lowercases a Content-Type and strips any "; charset=…"
// parameters so the map lookup matches "text/rtf; charset=utf-8".
func normalizeType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}
