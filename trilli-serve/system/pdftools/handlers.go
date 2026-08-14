package pdftools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"trilli/system/auth"
	"trilli/system/files"
)

// muxLike matches the subset of http.ServeMux that handler packages use.
type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Handlers exposes the PDF tools over HTTP. It reaches the tenant's file space
// only through files.Service, so per-tenant AES encryption, quota, and preview
// warming come for free.
type Handlers struct {
	files *files.Service
}

func NewHandlers(filesSvc *files.Service) *Handlers { return &Handlers{files: filesSvc} }

// Register wires one stateless endpoint per tool. Every tool follows the same
// contract: POST multipart/form-data → PDF in (Trilli file_ids and/or uploads)
// + params → result either streamed back (output=download) or written into the
// user's file space (output=save). Read-only "info" returns JSON.
func (h *Handlers) Register(m muxLike, requireAuth func(http.Handler) http.Handler) {
	reg := func(tool string, fn http.HandlerFunc) {
		m.Handle("POST /api/pdf/"+tool, requireAuth(fn))
	}
	reg("merge", h.handleMerge)
	reg("compress", h.handleCompress)
	reg("protect", h.handleProtect)
	reg("unlock", h.handleUnlock)
	reg("watermark", h.handleWatermark)
	reg("page-numbers", h.handlePageNumbers)
	reg("rotate", h.handleRotate)
	reg("nup", h.handleNUp)
	reg("info", h.handleInfo)
	reg("split", h.handleSplit)
	reg("delete-pages", h.handleDeletePages)
	reg("extract-pages", h.handleExtractPages)
	reg("image-to-pdf", h.handleImageToPdf)
	reg("extract-images", h.handleExtractImages)
	reg("pdf-to-image", h.handlePdfToImages)
}

// ── handlers ────────────────────────────────────────────────────────────────

func (h *Handlers) handleMerge(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok {
		return
	}
	if len(inputs) < 2 {
		writeJSON(w, http.StatusBadRequest, errorResp{"merge needs at least 2 files"})
		return
	}
	var buf bytes.Buffer
	if err := Merge(inputs, &buf); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), "merged.pdf")
}

func (h *Handlers) handleCompress(w http.ResponseWriter, r *http.Request) {
	h.single(w, r, "compressed.pdf", func(rs io.ReadSeeker, out io.Writer) error { return Compress(rs, out) })
}

func (h *Handlers) handleProtect(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	userPW := r.FormValue("user_password")
	if userPW == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"a password is required"})
		return
	}
	var buf bytes.Buffer
	if err := Protect(inputs[0], &buf, userPW, r.FormValue("owner_password")); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), "protected.pdf")
}

func (h *Handlers) handleUnlock(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	var buf bytes.Buffer
	if err := Unlock(inputs[0], &buf, r.FormValue("password")); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), "unlocked.pdf")
}

func (h *Handlers) handleWatermark(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"watermark text is required"})
		return
	}
	var buf bytes.Buffer
	if err := Watermark(inputs[0], &buf, text); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), "watermarked.pdf")
}

func (h *Handlers) handlePageNumbers(w http.ResponseWriter, r *http.Request) {
	h.single(w, r, "numbered.pdf", func(rs io.ReadSeeker, out io.Writer) error { return PageNumbers(rs, out) })
}

func (h *Handlers) handleRotate(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	rot := atoiDefault(r.FormValue("rotation"), 90)
	var buf bytes.Buffer
	if err := Rotate(inputs[0], &buf, rot); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), "rotated.pdf")
}

func (h *Handlers) handleNUp(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	var buf bytes.Buffer
	if err := NUp(inputs[0], &buf, atoiDefault(r.FormValue("n"), 2)); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), "n-up.pdf")
}

func (h *Handlers) handleInfo(w http.ResponseWriter, r *http.Request) {
	_, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	info, err := Info(inputs[0])
	if err != nil {
		opFailed(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *Handlers) handleSplit(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	var buf bytes.Buffer
	if err := SplitZip(inputs[0], atoiDefault(r.FormValue("span"), 1), &buf); err != nil {
		opFailed(w, err)
		return
	}
	h.deliverFile(w, r, id, buf.Bytes(), "split.zip", "application/zip")
}

func (h *Handlers) handleDeletePages(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	pages := strings.TrimSpace(r.FormValue("pages"))
	if pages == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"specify the pages to delete (e.g. 1-3,5)"})
		return
	}
	var buf bytes.Buffer
	if err := DeletePages(inputs[0], &buf, pages); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), "pages-removed.pdf")
}

func (h *Handlers) handleExtractPages(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	pages := strings.TrimSpace(r.FormValue("pages"))
	if pages == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"specify the pages to extract (e.g. 1,3-4)"})
		return
	}
	var buf bytes.Buffer
	if err := ExtractPages(inputs[0], &buf, pages); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), "extracted-pages.pdf")
}

func (h *Handlers) handleImageToPdf(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok {
		return
	}
	if len(inputs) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{"add at least one image"})
		return
	}
	readers := make([]io.Reader, len(inputs))
	for i := range inputs {
		readers[i] = inputs[i]
	}
	var buf bytes.Buffer
	if err := ImageToPDF(readers, &buf); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), "images.pdf")
}

func (h *Handlers) handleExtractImages(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	var buf bytes.Buffer
	if err := ExtractImagesZip(inputs[0], &buf); err != nil {
		opFailed(w, err)
		return
	}
	h.deliverFile(w, r, id, buf.Bytes(), "images.zip", "application/zip")
}

// handlePdfToImages rasterizes every page to a PNG (native, embedded-WASM PDFium)
// and returns a ZIP.
func (h *Handlers) handlePdfToImages(w http.ResponseWriter, r *http.Request) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	var buf bytes.Buffer
	if err := PDFToImagesZip(inputs[0], atoiDefault(r.FormValue("dpi"), 150), &buf); err != nil {
		opFailed(w, err)
		return
	}
	h.deliverFile(w, r, id, buf.Bytes(), "pages.zip", "application/zip")
}

// ── shared plumbing ───────────────────────────────────────────────────────────

// single runs a one-input → one-output PDF op and delivers the result.
func (h *Handlers) single(w http.ResponseWriter, r *http.Request, defName string, op func(io.ReadSeeker, io.Writer) error) {
	id, inputs, _, ok := h.begin(w, r)
	if !ok || !h.requireOne(w, inputs) {
		return
	}
	var buf bytes.Buffer
	if err := op(inputs[0], &buf); err != nil {
		opFailed(w, err)
		return
	}
	h.deliver(w, r, id, buf.Bytes(), defName)
}

// begin authenticates, parses the multipart form, and resolves every input PDF
// (Trilli files by id — folder-ACL gated — followed by ad-hoc uploads), each
// buffered into a seekable reader for pdfcpu. Returns ok=false having already
// written the error response.
func (h *Handlers) begin(w http.ResponseWriter, r *http.Request) (*auth.Identity, []io.ReadSeeker, []string, bool) {
	id, ok := auth.IdentityFromContext(r.Context())
	if !ok || id.Tenant == nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{"unauthorized"})
		return nil, nil, nil, false
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"invalid form data"})
		return nil, nil, nil, false
	}

	var inputs []io.ReadSeeker
	var names []string

	// 1) Files from the user's Trilli space, by id — gated by folder ACL.
	if raw := r.FormValue("file_ids"); raw != "" {
		var ids []int64
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResp{"file_ids must be a JSON array of ids"})
			return nil, nil, nil, false
		}
		for _, fid := range ids {
			if !h.canReadFile(r.Context(), id, fid) {
				writeJSON(w, http.StatusForbidden, errorResp{"you don't have access to one of the files"})
				return nil, nil, nil, false
			}
			f, rc, err := h.files.Download(r.Context(), id.Tenant.ID, fid)
			if err != nil {
				writeJSON(w, http.StatusNotFound, errorResp{"file not found"})
				return nil, nil, nil, false
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResp{"could not read file"})
				return nil, nil, nil, false
			}
			inputs = append(inputs, bytes.NewReader(data))
			names = append(names, f.Name)
		}
	}

	// 2) Ad-hoc uploads (drag/drop).
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["file"] {
			file, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(file)
			file.Close()
			if err != nil {
				continue
			}
			inputs = append(inputs, bytes.NewReader(data))
			names = append(names, fh.Filename)
		}
	}

	return id, inputs, names, true
}

func (h *Handlers) requireOne(w http.ResponseWriter, inputs []io.ReadSeeker) bool {
	if len(inputs) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResp{"no input file provided"})
		return false
	}
	return true
}

// deliver returns a PDF result.
func (h *Handlers) deliver(w http.ResponseWriter, r *http.Request, id *auth.Identity, data []byte, defaultName string) {
	h.deliverFile(w, r, id, data, defaultName, "application/pdf")
}

// deliverFile returns the result either as a download or, when output=save, writes
// it into the user's file space (folder-ACL checked) via files.Service — which
// encrypts, meters quota, and warms a preview automatically. contentType + the
// defaultName's extension cover non-PDF outputs (e.g. a ZIP of split pages).
func (h *Handlers) deliverFile(w http.ResponseWriter, r *http.Request, id *auth.Identity, data []byte, defaultName, contentType string) {
	name := outputName(r.FormValue("output_name"), defaultName)

	if r.FormValue("output") == "save" {
		var folderID *int64
		if v := r.FormValue("target_folder_id"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				folderID = &n
			}
		}
		if !id.Access.CanWrite(folderID) {
			writeJSON(w, http.StatusForbidden, errorResp{"you can't save to that folder"})
			return
		}
		f, err := h.files.Upload(r.Context(), files.UploadInput{
			TenantID:       id.Tenant.ID,
			UploaderID:     id.User.ID,
			Name:           name,
			ContentType:    contentType,
			Reader:         bytes.NewReader(data),
			ParentFolderID: folderID,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResp{"could not save: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"file_id": f.ID, "name": f.Name})
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

func (h *Handlers) canReadFile(ctx context.Context, id *auth.Identity, fileID int64) bool {
	_, folderID, err := h.files.FileMeta(ctx, id.Tenant.ID, fileID)
	if err != nil {
		return false
	}
	return id.Access.CanRead(folderID)
}

// ── small helpers ─────────────────────────────────────────────────────────────

type errorResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// opFailed maps an engine error to a 422 — the input wasn't processable (most
// often a corrupt/encrypted PDF or a bad parameter).
func opFailed(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusUnprocessableEntity, errorResp{"couldn't process the PDF: " + err.Error()})
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

// outputName sanitizes the requested name to a bare filename with the SAME
// extension as def (".pdf", ".zip", …), falling back to def. Strips any path
// components so a client can't influence the path.
func outputName(requested, def string) string {
	ext := strings.ToLower(path.Ext(def))
	if ext == "" {
		ext = ".pdf"
	}
	name := strings.TrimSpace(requested)
	if name == "" {
		name = def
	}
	name = path.Base(filepath_ToSlash(name))
	if !strings.EqualFold(path.Ext(name), ext) {
		name = strings.TrimSuffix(name, path.Ext(name)) + ext
	}
	return name
}

// filepath_ToSlash normalizes backslashes so path.Base also strips Windows-style
// path components from a client-supplied name.
func filepath_ToSlash(s string) string { return strings.ReplaceAll(s, `\`, "/") }
