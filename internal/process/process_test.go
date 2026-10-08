package process

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/audemed44/docvault/internal/store"
)

// withOrientation adds an EXIF APP1 segment with the orientation tag.
func withOrientation(jpg []byte, o uint16) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM\x00\x2a")
	binary.Write(&tiff, binary.BigEndian, uint32(8))
	binary.Write(&tiff, binary.BigEndian, uint16(1))
	binary.Write(&tiff, binary.BigEndian, []uint16{0x0112, 3})
	binary.Write(&tiff, binary.BigEndian, uint32(1))
	binary.Write(&tiff, binary.BigEndian, []uint16{o, 0})
	binary.Write(&tiff, binary.BigEndian, uint32(0))
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write(jpg[:2])
	out.Write([]byte{0xFF, 0xE1})
	binary.Write(&out, binary.BigEndian, uint16(len(seg)+2))
	out.Write(seg)
	out.Write(jpg[2:])
	return out.Bytes()
}

func testJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, 0, color.RGBA{255, 0, 0, 255})
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

func TestExifOrientation(t *testing.T) {
	plain := testJPEG(40, 20)
	if o := exifOrientation(plain); o != 1 {
		t.Fatalf("no EXIF: %d", o)
	}
	for _, o := range []uint16{3, 6, 8} {
		if got := exifOrientation(withOrientation(plain, o)); got != int(o) {
			t.Fatalf("orientation %d read as %d", o, got)
		}
	}
	if o := exifOrientation([]byte("not a jpeg")); o != 1 {
		t.Fatal(o)
	}
}

func need(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s isn't installed", tool)
		}
	}
}

func TestImagePDF(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	os.WriteFile(src, withOrientation(testJPEG(400, 200), 6), 0o644)
	out := filepath.Join(dir, "doc.pdf")
	if err := jpegToPDF(src, out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) || !bytes.Contains(data, []byte("/MediaBox [0 0 421.00 842.00]")) {
		t.Fatalf("turned page:\n%.400s", data)
	}
	need(t, "pdfinfo")
	if n, err := pageCount(context.Background(), out); err != nil || n != 1 {
		t.Fatalf("pdfinfo: %d %v", n, err)
	}
}

// textPDF makes a PDF with a real text layer.
func textPDF(lines ...string) []byte {
	var content strings.Builder
	content.WriteString("BT /F1 28 Tf 60 760 Td 36 TL\n")
	for _, l := range lines {
		fmt.Fprintf(&content, "(%s) Tj T*\n", l)
	}
	content.WriteString("ET\n")
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{}
	for i, o := range objs {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func setup(t *testing.T) (*Processor, *store.Store, *store.User) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	u := &store.User{Username: "me", Name: "Me"}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return New(Options{Store: st, Files: filepath.Join(dir, "files"), Cache: filepath.Join(dir, "cache")}), st, u
}

// addFile stores data as a document and runs its job.
func addFile(t *testing.T, p *Processor, st *store.Store, u *store.User, name, mime string, data []byte) *store.Document {
	t.Helper()
	ctx := context.Background()
	d := &store.Document{OwnerID: u.ID, Title: name, DocDate: "2024-01-01", FileName: name, Mime: mime,
		SHA256: fmt.Sprintf("%x", data[:min(len(data), 64)]) + name}
	if err := st.CreateDocument(ctx, d, u.ID); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(fmt.Sprint(d.ID), name)
	os.MkdirAll(filepath.Join(p.Files, fmt.Sprint(d.ID)), 0o755)
	os.WriteFile(filepath.Join(p.Files, rel), data, 0o644)
	st.SetFilePath(ctx, d.ID, rel)
	job, err := st.ClaimJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishJob(ctx, job.ID, p.process(ctx, job)); err != nil {
		t.Fatal(err)
	}
	got, err := st.Document(ctx, u.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTextLayer(t *testing.T) {
	need(t, "pdfinfo", "pdftoppm", "pdftotext")
	p, st, u := setup(t)
	d := addFile(t, p, st, u, "policy.pdf", "application/pdf", textPDF("Motor insurance policy", "Policy number 4471-0099", "Valid until 2025"))
	if d.Status != "ready" || d.Pages != 1 || d.TextSource != "pdf" {
		t.Fatalf("processed %+v", d)
	}
	if _, err := os.Stat(p.Thumb(d.ID)); err != nil {
		t.Fatal("no thumbnail")
	}
	if docs, _, _ := st.Search(context.Background(), u.ID, store.Filter{Query: "4471"}); len(docs) != 1 {
		t.Fatal("text not searchable")
	}
}

func TestOCR(t *testing.T) {
	need(t, "pdfinfo", "pdftoppm", "pdftotext", "tesseract")
	p, st, u := setup(t)
	// A scan: the text PDF rendered to a PNG, so it has no text layer.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "t.pdf"), textPDF("Discharge summary", "Patient ward seven"), 0o644)
	if _, err := run(context.Background(), "pdftoppm", "-r", "150", "-png", "-singlefile", filepath.Join(dir, "t.pdf"), filepath.Join(dir, "scan")); err != nil {
		t.Fatal(err)
	}
	scan, _ := os.ReadFile(filepath.Join(dir, "scan.png"))
	if _, err := png.DecodeConfig(bytes.NewReader(scan)); err != nil {
		t.Fatal(err)
	}
	d := addFile(t, p, st, u, "scan.png", "image/png", scan)
	if d.Status != "ready" || d.TextSource != "ocr" || d.OCRLang == "" {
		t.Fatalf("processed %+v", d)
	}
	text, _ := st.DocumentText(context.Background(), u.ID, d.ID)
	if !strings.Contains(strings.ToLower(text), "discharge summary") {
		t.Fatalf("OCR text %q", text)
	}
	if _, err := os.Stat(p.CachePDF(d.ID)); err != nil {
		t.Fatal("no PDF for the photo")
	}

	d = addFile(t, p, st, u, "broken.pdf", "application/pdf", []byte("%PDF-1.4 nonsense"))
	if d.Status != "failed" || !strings.Contains(d.Error, "not a readable PDF") {
		t.Fatalf("broken PDF %+v", d)
	}
}

// Several workers share the queue: every document is processed once.
func TestWorkers(t *testing.T) {
	p, st, u := setup(t)
	p.Workers = 3
	ctx := context.Background()
	var ids []int64
	for i := range 9 {
		name := fmt.Sprintf("doc%d.pdf", i)
		d := &store.Document{OwnerID: u.ID, Title: name, DocDate: "2024-01-01", FileName: name, Mime: "application/pdf", SHA256: name}
		if err := st.CreateDocument(ctx, d, u.ID); err != nil {
			t.Fatal(err)
		}
		os.MkdirAll(filepath.Join(p.Files, fmt.Sprint(d.ID)), 0o755)
		os.WriteFile(filepath.Join(p.Files, fmt.Sprint(d.ID), name), textPDF(name), 0o644)
		st.SetFilePath(ctx, d.ID, filepath.Join(fmt.Sprint(d.ID), name))
		ids = append(ids, d.ID)
	}
	run, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { p.Run(run); close(done) }()
	deadline := time.Now().Add(30 * time.Second)
	for _, id := range ids {
		for {
			d, err := st.Document(ctx, u.ID, id)
			if err != nil {
				t.Fatal(err)
			}
			if d.Status == "ready" || d.Status == "failed" { // failed without poppler
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("document %d is still %s", id, d.Status)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	stop()
	<-done
}

// Pages that say they're huge (photos at 72 dpi) are rendered smaller.
func TestPageDPI(t *testing.T) {
	need(t, "pdfinfo")
	dir := t.TempDir()
	a4 := filepath.Join(dir, "a4.pdf")
	os.WriteFile(a4, textPDF("A4"), 0o644)
	big := filepath.Join(dir, "big.pdf")
	os.WriteFile(big, bytes.Replace(textPDF("big"), []byte("0 0 595 842"), []byte("0 0 2636 3708"), 1), 0o644)
	ctx := context.Background()
	if got := pageDPI(ctx, a4, 1); got != 300 {
		t.Fatalf("A4 at %d dpi", got)
	}
	if got := pageDPI(ctx, big, 1); got != 81 {
		t.Fatalf("big page at %d dpi", got)
	}
}
