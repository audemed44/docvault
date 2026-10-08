package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/audemed44/docvault/internal/store"
)

// Every way in (upload, folder import, Foyer's Drop) goes through here:
// the file is spooled to disk while it's hashed, checked, then moved to
// files/<id>/<name> unchanged.

// maxFile is the largest single file accepted.
const maxFile = 200 << 20

// spooled is an incoming file on disk, not yet a document.
type spooled struct {
	path string
	name string
	mime string
	size int64
	sha  string
}

func (f *spooled) discard() { os.Remove(f.path) }

var errTooBig = fmt.Errorf("the file is over %d MB", maxFile>>20)

// spool copies r to a temporary file, hashing it and sniffing its type.
func (s *Server) spool(r io.Reader, name string) (*spooled, error) {
	tmp, err := os.CreateTemp(s.dir("tmp"), "upload-*")
	if err != nil {
		return nil, err
	}
	f := &spooled{path: tmp.Name(), name: name}
	h := sha256.New()
	var head bytes.Buffer
	n, err := io.Copy(io.MultiWriter(tmp, h, &limitedBuffer{&head, 512}), io.LimitReader(r, maxFile+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxFile {
		err = errTooBig
	}
	if err != nil {
		f.discard()
		return nil, err
	}
	f.size, f.sha, f.mime = n, hex.EncodeToString(h.Sum(nil)), sniff(head.Bytes())
	return f, nil
}

type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// sniff names the file type from its first bytes: PDF, JPEG, PNG or HEIC,
// or "" for anything else.
func sniff(head []byte) string {
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		switch string(head[8:12]) {
		case "heic", "heix", "heim", "heis", "hevc", "mif1", "msf1":
			return "image/heic"
		}
	}
	switch t := http.DetectContentType(head); t {
	case "application/pdf", "image/jpeg", "image/png":
		return t
	}
	return ""
}

var extFor = map[string]string{
	"application/pdf": ".pdf", "image/jpeg": ".jpg", "image/png": ".png", "image/heic": ".heic",
}

// ingestOptions describe where a file goes and what's known about it.
type ingestOptions struct {
	User       *store.User
	Family     bool
	Title      string
	CategoryID int64
	Tags       []string
	DocDate    string
	Notes      string
	// Modified is the file's own date, used when nothing better is known.
	Modified time.Time
}

type ingestResult struct {
	Name     string          `json:"name"`
	Status   string          `json:"status"` // added, duplicate, skipped or failed
	Message  string          `json:"message,omitempty"`
	Document *store.Document `json:"document,omitempty"`
}

// commit turns a spooled file into a document and queues it.
func (s *Server) commit(ctx context.Context, f *spooled, o ingestOptions) ingestResult {
	res := ingestResult{Name: f.name}
	defer f.discard()
	if f.mime == "" {
		res.Status, res.Message = "skipped", "not a PDF or photo (JPEG, PNG, HEIC)"
		return res
	}
	if f.size == 0 {
		res.Status, res.Message = "skipped", "the file is empty"
		return res
	}
	if dup, err := s.Store.Duplicate(ctx, o.Family, o.User.ID, f.sha); err == nil {
		res.Status, res.Message, res.Document = "duplicate", "already saved as “"+dup.Title+"”", dup
		return res
	} else if !errors.Is(err, store.ErrNotFound) {
		res.Status, res.Message = "failed", err.Error()
		return res
	}

	d := &store.Document{
		Family: o.Family, OwnerID: o.User.ID, Title: o.Title, CategoryID: o.CategoryID, Tags: o.Tags,
		DocDate: o.DocDate, Notes: o.Notes, FileName: f.name, Mime: f.mime, Size: f.size, SHA256: f.sha,
	}
	if d.Title == "" {
		d.Title = titleFromName(f.name)
	}
	if d.DocDate == "" {
		d.DocDate = dateFromName(f.name)
	}
	if d.DocDate == "" && !o.Modified.IsZero() {
		d.DocDate = o.Modified.Format(time.DateOnly)
	}
	if d.DocDate == "" {
		d.DocDate = time.Now().Format(time.DateOnly)
	}
	if err := s.Store.CreateDocument(ctx, d, o.User.ID); err != nil {
		res.Status, res.Message = "failed", err.Error()
		return res
	}
	rel := filepath.Join(strconv.FormatInt(d.ID, 10), safeName(f.name, extFor[f.mime]))
	dst := filepath.Join(s.dir("files"), rel)
	err := os.MkdirAll(filepath.Dir(dst), 0o755)
	if err == nil {
		err = moveFile(f.path, dst)
	}
	if err == nil {
		err = s.Store.SetFilePath(ctx, d.ID, rel)
	}
	if err != nil {
		_ = s.Store.RemoveDocument(ctx, d.ID)
		os.RemoveAll(filepath.Dir(dst))
		res.Status, res.Message = "failed", err.Error()
		return res
	}
	d.FilePath = rel
	s.Processor.Wake()
	res.Status, res.Message, res.Document = "added", "saved “"+d.Title+"”", d
	return res
}

// moveFile renames, or copies when tmp and files are different disks.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// safeName keeps the uploaded name readable on disk without path tricks.
func safeName(name, ext string) string {
	base := strings.TrimSuffix(filepath.Base(strings.ReplaceAll(name, `\`, "/")), filepath.Ext(name))
	base = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, base)
	base = strings.Trim(base, ". ")
	if len(base) > 100 {
		base = strings.ToValidUTF8(base[:100], "")
	}
	if base == "" {
		base = "document"
	}
	return base + ext
}

func titleFromName(name string) string {
	base := filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	t := strings.TrimSpace(strings.TrimSuffix(base, filepath.Ext(base)))
	if t == "" || t == "." {
		return time.Now().Format("2 Jan 2006")
	}
	return t
}

var (
	// CamScanner names its exports "CamScanner 03-15-2021 10.22".
	camScannerDate = regexp.MustCompile(`(?i)camscanner\s+(\d{2})-(\d{2})-(\d{4})`)
	isoDate        = regexp.MustCompile(`(^|[^\d])(\d{4})-(\d{2})-(\d{2})([^\d]|$)`)
)

// dateFromName finds a document date in a file name, or "".
func dateFromName(name string) string {
	var y, m, d string
	if p := camScannerDate.FindStringSubmatch(name); p != nil {
		m, d, y = p[1], p[2], p[3]
	} else if p := isoDate.FindStringSubmatch(name); p != nil {
		y, m, d = p[2], p[3], p[4]
	} else {
		return ""
	}
	date := y + "-" + m + "-" + d
	if t, err := time.Parse(time.DateOnly, date); err != nil || t.Year() < 1900 || t.After(time.Now().AddDate(1, 0, 0)) {
		return ""
	}
	return date
}
