package officeapi

import (
	"testing"
)

// TestWithExt checks the name/extension normalization used by Save As +
// Download so "Report.docx" + "doc" → "Report.doc" (not "Report.docx.doc").
func TestWithExt(t *testing.T) {
	cases := []struct{ in, ext, want string }{
		{"Report", "doc", "Report.doc"},
		{"Report.docx", "doc", "Report.doc"},
		{"Report.DOCX", "docx", "Report.docx"},
		{"Q3 Deck.pptx", "ppt", "Q3 Deck.ppt"},
		{"Book", "xlsx", "Book.xlsx"},
		{"notes.docx", "docx", "notes.docx"},
	}
	for _, c := range cases {
		if got := withExt(c.in, c.ext); got != c.want {
			t.Errorf("withExt(%q, %q) = %q, want %q", c.in, c.ext, got, c.want)
		}
	}
}

// TestHasOfficeExt checks the office-extension detector.
func TestHasOfficeExt(t *testing.T) {
	if !hasOfficeExt("Report.docx") {
		t.Error("Report.docx should be detected as an office ext")
	}
	if !hasOfficeExt("DATA.XLSX") {
		t.Error("DATA.XLSX should be detected (case-insensitive)")
	}
	if hasOfficeExt("archive.zip") {
		t.Error("archive.zip should NOT be detected as an office ext")
	}
}

// TestResolveFormat checks the format lookup + native default.
func TestResolveFormat(t *testing.T) {
	if f, ok := resolveFormat("docs", ""); !ok || f.ext != "docx" {
		t.Errorf("empty format should default to native docx, got %v ok=%v", f, ok)
	}
	if f, ok := resolveFormat("docs", "doc"); !ok || f.ext != "doc" {
		t.Errorf("doc should resolve, got %v ok=%v", f, ok)
	}
	if f, ok := resolveFormat("docs", "pdf"); ok {
		t.Errorf("pdf should not be a supported format, got %v", f)
	}
	// leading-dot tolerance
	if f, ok := resolveFormat("docs", ".docx"); !ok || f.ext != "docx" {
		t.Errorf(".docx should resolve to docx, got %v ok=%v", f, ok)
	}
	// unknown app
	if _, ok := resolveFormat("nope", ""); ok {
		t.Error("unknown app should not resolve")
	}
}

// TestSanitizeDownloadName ensures the Content-Disposition filename is safe.
func TestSanitizeDownloadName(t *testing.T) {
	if got := sanitizeDownloadName(`a"b\c`); got != "abc" {
		t.Errorf(`sanitizeDownloadName got %q, want "abc"`, got)
	}
	if got := sanitizeDownloadName(""); got != "document" {
		t.Errorf("empty name should fall back to document, got %q", got)
	}
}

// TestFirstNonEmpty checks the name fallback helper.
func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "fallback"); got != "fallback" {
		t.Errorf("firstNonEmpty empty→fallback got %q", got)
	}
	if got := firstNonEmpty("  ", "fallback"); got != "fallback" {
		t.Errorf("firstNonEmpty whitespace→fallback got %q", got)
	}
	if got := firstNonEmpty("kept", "ignored"); got != "kept" {
		t.Errorf("firstNonEmpty kept got %q", got)
	}
}
