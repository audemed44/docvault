package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/docvault/internal/process"
	"github.com/audemed44/docvault/internal/store"
)

// ── Upload ──────────────────────────────────────────────────────────────

// maxUpload caps one upload request (several files).
const maxUpload = 1 << 30

// upload takes one or more files (field "file") with optional title,
// category (name or ID), tags (comma-separated), date, notes, space
// ("family" or "private") and modified (the file's own time, in ms).
//
// The iOS Shortcut reads the answer as text: "ok: saved “Title”" or
// "error: …". Callers asking for JSON get the results per file.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	wantJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	fail := func(status int, msg string) {
		if wantJSON {
			writeError(w, status, msg)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintf(w, "error: %s\n", msg)
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	fields, files, err := s.readUpload(r)
	defer func() {
		for _, f := range files {
			f.discard()
		}
	}()
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	if len(files) == 0 {
		fail(http.StatusBadRequest, "no file: send it in the form field “file”")
		return
	}
	o, note, err := s.uploadOptions(r, currentUser(r), fields)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}

	results := make([]ingestResult, 0, len(files))
	for i, f := range files {
		fo := o
		if len(files) > 1 && o.Title != "" {
			fo.Title = fmt.Sprintf("%s (%d of %d)", o.Title, i+1, len(files))
		}
		results = append(results, s.commit(r.Context(), f, fo))
	}
	files = nil // committed files clean up after themselves

	status, msg := summarize(results)
	if note != "" {
		msg += " (" + note + ")"
	}
	if wantJSON {
		writeJSON(w, status, map[string]any{"message": msg, "results": results})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
}

// summarize sums up the results in one line for the Shortcut.
func summarize(results []ingestResult) (int, string) {
	if len(results) == 1 {
		r := results[0]
		switch r.Status {
		case "added", "duplicate":
			return http.StatusOK, "ok: " + r.Message
		case "skipped":
			return http.StatusUnsupportedMediaType, "error: " + r.Message
		}
		return http.StatusInternalServerError, "error: " + r.Message
	}
	count := map[string]int{}
	for _, r := range results {
		count[r.Status]++
	}
	parts := []string{fmt.Sprintf("saved %d", count["added"])}
	if n := count["duplicate"]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d already saved", n))
	}
	if n := count["skipped"] + count["failed"]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", n))
	}
	if count["added"]+count["duplicate"] == 0 {
		return http.StatusBadRequest, "error: " + strings.Join(parts, ", ")
	}
	return http.StatusOK, "ok: " + strings.Join(parts, ", ")
}

// readUpload spools every file part to disk and collects the other fields
// (the Shortcut may send them after the file).
func (s *Server) readUpload(r *http.Request) (map[string]string, []*spooled, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, errors.New("expected a multipart form upload")
	}
	fields := map[string]string{}
	var files []*spooled
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, files, uploadErr(err)
		}
		if part.FileName() == "" {
			if part.FormName() == "file" { // a file sent as a plain field
				f, err := s.spool(part, "document")
				if err != nil {
					return nil, files, uploadErr(err)
				}
				files = append(files, f)
				continue
			}
			v, err := io.ReadAll(io.LimitReader(part, 64<<10))
			if err != nil {
				return nil, files, uploadErr(err)
			}
			fields[part.FormName()] = strings.TrimSpace(string(v))
			continue
		}
		f, err := s.spool(part, partName(part))
		if err != nil {
			return nil, files, uploadErr(err)
		}
		files = append(files, f)
	}
	return fields, files, nil
}

func partName(p *multipart.Part) string {
	if n := p.FileName(); n != "" {
		return n
	}
	return "document"
}

func uploadErr(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return fmt.Errorf("the upload is over %d MB", maxUpload>>20)
	}
	return fmt.Errorf("upload: %w", err)
}

// uploadOptions reads the form fields. note says what was ignored (an
// unknown category), so the Shortcut can show it.
func (s *Server) uploadOptions(r *http.Request, u *store.User, f map[string]string) (ingestOptions, string, error) {
	o := ingestOptions{User: u, Title: f["title"], Notes: f["notes"], Family: f["space"] == "family"}
	note := ""
	if len(o.Title) > 200 {
		return o, "", errors.New("the title is too long")
	}
	if c := f["category"]; c != "" && !strings.EqualFold(c, "uncategorised") && !strings.EqualFold(c, "inbox") {
		if id, err := strconv.ParseInt(c, 10, 64); err == nil {
			o.CategoryID = id
		} else if id, err := s.Store.CategoryByName(r.Context(), c); err == nil {
			o.CategoryID = id
		} else {
			note = "unknown category “" + c + "”, so it's in the inbox"
		}
	}
	if t := f["tags"]; t != "" {
		o.Tags = strings.Split(t, ",")
	}
	if d := f["date"]; d != "" {
		if _, err := time.Parse(time.DateOnly, d); err != nil {
			return o, "", errors.New("date must look like 2024-03-15")
		}
		o.DocDate = d
	}
	if m, err := strconv.ParseInt(f["modified"], 10, 64); err == nil && m > 0 {
		o.Modified = time.UnixMilli(m)
	}
	return o, note, nil
}

// ── Library ─────────────────────────────────────────────────────────────

// filterOf reads a library filter from the query string.
func filterOf(r *http.Request) store.Filter {
	q := r.URL.Query()
	f := store.Filter{Query: q.Get("q"), Space: q.Get("space"), Tag: q.Get("tag"), Person: q.Get("person"), Year: q.Get("year"),
		Expiring: q.Get("expiring") == "1", Status: q.Get("status"),
		Suggested: q.Get("suggested") == "1", Unclassified: q.Get("unclassified") == "1",
		Warnings: q.Get("warnings") == "1"}
	switch c := q.Get("category"); c {
	case "", "all":
	case "none":
		f.Category = -1
	default:
		f.Category, _ = strconv.ParseInt(c, 10, 64)
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	return f
}

func (s *Server) listDocuments(w http.ResponseWriter, r *http.Request) {
	docs, total, err := s.Store.Search(r.Context(), currentUser(r).ID, filterOf(r))
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": docs, "total": total})
}

func (s *Server) facets(w http.ResponseWriter, r *http.Request) {
	f, err := s.Store.Facets(r.Context(), currentUser(r).ID)
	if err != nil {
		storeError(w, err)
		return
	}
	set, err := s.Store.Settings(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	names := []string{}
	for _, p := range set.People {
		names = append(names, p.Name)
	}
	f.SplitPeople(names)
	writeJSON(w, http.StatusOK, f)
}

// document loads the {id} document for the current user.
func (s *Server) document(w http.ResponseWriter, r *http.Request) (*store.Document, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	d, err := s.Store.Document(r.Context(), currentUser(r).ID, id)
	if err != nil {
		storeError(w, err)
		return nil, false
	}
	return d, true
}

func (s *Server) getDocument(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.document(w, r); ok {
		writeJSON(w, http.StatusOK, d)
	}
}

func (s *Server) documentText(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	text, err := s.Store.DocumentText(r.Context(), currentUser(r).ID, id)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

func validDate(d string, optional bool) bool {
	if d == "" {
		return optional
	}
	_, err := time.Parse(time.DateOnly, d)
	return err == nil
}

func (s *Server) updateDocument(w http.ResponseWriter, r *http.Request) {
	d, ok := s.document(w, r)
	if !ok {
		return
	}
	var body struct {
		Title      string   `json:"title"`
		CategoryID int64    `json:"category_id"`
		DocDate    string   `json:"doc_date"`
		Expires    string   `json:"expires"`
		Notes      string   `json:"notes"`
		Tags       []string `json:"tags"`
		Family     bool     `json:"family"`
	}
	if !readJSON(w, r, 256<<10, &body) {
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	switch {
	case body.Title == "" || len(body.Title) > 200:
		writeError(w, http.StatusBadRequest, "a title is required (up to 200 characters)")
		return
	case !validDate(body.DocDate, false) || !validDate(body.Expires, true):
		writeError(w, http.StatusBadRequest, "dates must look like 2024-03-15")
		return
	case len(body.Notes) > 20_000:
		writeError(w, http.StatusBadRequest, "the notes are too long")
		return
	}
	d.Title, d.CategoryID, d.DocDate, d.Expires = body.Title, body.CategoryID, body.DocDate, body.Expires
	d.Notes, d.Tags, d.Family = strings.TrimSpace(body.Notes), body.Tags, body.Family
	s.saveDocument(w, r, d)
}

func (s *Server) saveDocument(w http.ResponseWriter, r *http.Request, d *store.Document) {
	me := currentUser(r)
	if err := s.Store.UpdateDocument(r.Context(), me.ID, d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "the same file is already there")
			return
		}
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			writeError(w, http.StatusBadRequest, "unknown category")
			return
		}
		storeError(w, err)
		return
	}
	d, err := s.Store.Document(r.Context(), me.ID, d.ID)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := s.Store.DeleteDocument(r.Context(), currentUser(r).ID, id)
	if err != nil {
		storeError(w, err)
		return
	}
	s.removeFiles(d.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeFiles(id int64) {
	n := strconv.FormatInt(id, 10)
	os.RemoveAll(filepath.Join(s.dir("files"), n))
	os.RemoveAll(filepath.Join(s.dir("cache"), n))
}

func (s *Server) reprocess(w http.ResponseWriter, r *http.Request) {
	d, ok := s.document(w, r)
	if !ok {
		return
	}
	var body struct {
		OCR  bool   `json:"ocr"`
		Lang string `json:"lang"`
	}
	if !readJSON(w, r, 4<<10, &body) {
		return
	}
	if body.Lang != "" && !validLangs(body.Lang) {
		writeError(w, http.StatusBadRequest, "unknown OCR language "+body.Lang)
		return
	}
	if err := s.Store.Requeue(r.Context(), currentUser(r).ID, d.ID, body.OCR, body.Lang, false); err != nil {
		storeError(w, err)
		return
	}
	s.Processor.Wake()
	d.Status, d.Error = "pending", ""
	writeJSON(w, http.StatusOK, d)
}

// validLangs checks "eng+hin" against what Tesseract has installed (any
// when it isn't installed, as in development).
func validLangs(spec string) bool {
	installed := process.Languages()
	for l := range strings.SplitSeq(spec, "+") {
		if l == "" || len(l) > 20 || (installed != nil && !slices.Contains(installed, l)) {
			return false
		}
	}
	return true
}

// applySuggestion applies the classifier's suggestion to one document.
func (s *Server) applySuggestion(w http.ResponseWriter, r *http.Request) {
	d, ok := s.document(w, r)
	if !ok {
		return
	}
	if d.Suggestion == nil {
		writeError(w, http.StatusNotFound, "no suggestion")
		return
	}
	s.suggest(d)
	if err := s.Store.ClearSuggestion(r.Context(), currentUser(r).ID, d.ID); err != nil {
		storeError(w, err)
		return
	}
	s.saveDocument(w, r, d)
}

// suggest changes d as its suggestion says: its title, date, expiry and
// category, and its tags added to the document's.
func (s *Server) suggest(d *store.Document) {
	sg := d.Suggestion
	if sg.DocDate != "" {
		d.DocDate = sg.DocDate
	}
	if sg.Expires != "" {
		d.Expires = sg.Expires
	}
	if sg.Category != "" {
		if id, err := s.Store.CategoryByName(context.Background(), sg.Category); err == nil {
			d.CategoryID = id
		}
	}
	d.Tags = append(append(d.Tags, sg.Tags...), sg.NewTags...)
}

// selection is the documents a bulk action is for: the IDs in a JSON body
// ({"ids": [...]}) when there is one, else every document matching the
// filter in the query string.
func (s *Server) selection(w http.ResponseWriter, r *http.Request, f store.Filter) ([]int64, bool) {
	if r.ContentLength > 0 || r.Header.Get("Content-Type") != "" {
		var body struct {
			IDs []int64 `json:"ids"`
		}
		if !readJSON(w, r, 256<<10, &body) {
			return nil, false
		}
		if len(body.IDs) > 5000 {
			writeError(w, http.StatusBadRequest, "too many documents at once")
			return nil, false
		}
		return body.IDs, true
	}
	ids, err := s.Store.MatchingIDs(r.Context(), currentUser(r).ID, f)
	if err != nil {
		storeError(w, err)
		return nil, false
	}
	return ids, true
}

// applySuggestions applies the suggestions of the selected documents.
func (s *Server) applySuggestions(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	f := filterOf(r)
	f.Suggested = true
	ids, ok := s.selection(w, r, f)
	if !ok {
		return
	}
	applied := 0
	for _, id := range ids {
		d, err := s.Store.Document(r.Context(), me.ID, id)
		if err != nil || d.Suggestion == nil {
			continue
		}
		s.suggest(d)
		if err := s.Store.UpdateDocument(r.Context(), me.ID, d); err != nil {
			slog.Warn("apply suggestion", "document", id, "err", err)
			continue
		}
		if err := s.Store.ClearSuggestion(r.Context(), me.ID, id); err == nil {
			applied++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"applied": applied})
}

// requestSuggestions asks for suggestions for the selected documents: they
// go through processing again (keeping their OCR text), this time with the
// classifier.
func (s *Server) requestSuggestions(w http.ResponseWriter, r *http.Request) {
	if s.Processor.Classifier == nil {
		writeError(w, http.StatusConflict, "suggestions are off: set DOCVAULT_LLM_KEY and DOCVAULT_LLM_MODEL")
		return
	}
	me := currentUser(r)
	ids, ok := s.selection(w, r, filterOf(r))
	if !ok {
		return
	}
	queued := 0
	for _, id := range ids {
		if err := s.Store.Requeue(r.Context(), me.ID, id, false, "", true); err == nil {
			queued++
		}
	}
	s.Processor.Wake()
	writeJSON(w, http.StatusOK, map[string]int{"queued": queued})
}

// classifierInput is the masked text the classifier was last sent.
func (s *Server) classifierInput(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	text, err := s.Store.ClassifierInput(r.Context(), currentUser(r).ID, id)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

func (s *Server) dismissSuggestion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.ClearSuggestion(r.Context(), currentUser(r).ID, id); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Files ───────────────────────────────────────────────────────────────

// downloadName is the file name a document is saved or shared under.
func downloadName(title, ext string) string {
	return safeName(title+ext, ext)
}

// serveFile sends a file. With asPDF, a PDF is sent from its header on, in
// case there's junk before it (see pdfJunkMax); otherwise byte for byte.
func serveFile(w http.ResponseWriter, r *http.Request, path, mime, name string, attachment, asPDF bool) {
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "the file isn't there")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	disposition := "inline"
	if attachment {
		disposition = "attachment"
	}
	h := w.Header()
	// Browsers' PDF viewers run under the page's CSP; this is a file, not the app.
	h.Del("Content-Security-Policy")
	h.Set("Content-Type", mime)
	h.Set("Content-Disposition", disposition+"; filename*=UTF-8''"+url.PathEscape(name))
	h.Set("Cache-Control", "private, no-cache")
	var content io.ReadSeeker = f
	if asPDF && mime == "application/pdf" {
		head := make([]byte, pdfJunkMax)
		n, _ := f.ReadAt(head, 0)
		if at := pdfStart(head[:n]); at > 0 {
			content = io.NewSectionReader(f, int64(at), info.Size()-int64(at))
		}
	}
	http.ServeContent(w, r, "", info.ModTime(), content)
}

// documentFile serves the document as a PDF: the original, or the PDF
// made from a photo (the photo itself until that's ready).
func (s *Server) documentFile(w http.ResponseWriter, r *http.Request) {
	d, ok := s.document(w, r)
	if !ok {
		return
	}
	download := r.URL.Query().Get("download") == "1"
	if process.IsImage(d.Mime) {
		pdf := s.Processor.CachePDF(d.ID)
		if _, err := os.Stat(pdf); err == nil {
			serveFile(w, r, pdf, "application/pdf", downloadName(d.Title, ".pdf"), download, true)
			return
		}
		if d.Mime == "image/heic" {
			writeError(w, http.StatusConflict, "still converting the photo; try again in a moment")
			return
		}
	}
	serveFile(w, r, filepath.Join(s.dir("files"), d.FilePath), d.Mime, downloadName(d.Title, extFor[d.Mime]), download, true)
}

// documentOriginal serves the uploaded file byte for byte.
func (s *Server) documentOriginal(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.document(w, r); ok {
		serveFile(w, r, filepath.Join(s.dir("files"), d.FilePath), d.Mime, d.FileName, true, false)
	}
}

func (s *Server) documentThumb(w http.ResponseWriter, r *http.Request) {
	d, ok := s.document(w, r)
	if !ok {
		return
	}
	f, err := os.Open(s.Processor.Thumb(d.ID))
	if err != nil {
		writeError(w, http.StatusNotFound, "no thumbnail yet")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", time.Time{}, f)
}
