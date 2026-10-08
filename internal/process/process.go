// Package process turns stored documents into something viewable and
// searchable: a PDF for photos, a page-one thumbnail, the page count, and
// the text (the PDF's own text layer, or Tesseract OCR when there's none).
// Then the optional classifier suggests a category and tags.
//
// In the background, with poppler and tesseract as short-lived child
// processes (one page at a time), so the server itself stays small.
package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/audemed44/docvault/internal/store"
)

const (
	// maxOCRPages caps OCR for very long scans (the text layer has no cap).
	maxOCRPages = 100
	// maxText caps the stored text per document.
	maxText = 4 << 20
	// stepTimeout bounds every child process.
	stepTimeout = 5 * time.Minute
)

// Worker limits: reading (OCR) is memory-bound, a suggestion is a small
// HTTP request that mostly waits on the model.
const (
	MaxOCRWorkers         = 8
	MaxSuggestWorkers     = 32
	DefaultSuggestWorkers = 8
)

type Options struct {
	Store *store.Store
	// Files holds the originals, Cache the derived files (photo PDFs and
	// thumbnails), which can be rebuilt.
	Files, Cache string
	// Classifier, when set, suggests a category and tags for each document.
	Classifier Classifier
	// Workers is how many documents are read at once when the settings
	// don't say (at least 1). Each OCR peaks around 460 MB.
	Workers int
}

type Processor struct {
	Options
	readWake, suggestWake chan struct{}
}

func New(o Options) *Processor {
	return &Processor{Options: o, readWake: make(chan struct{}, 1), suggestWake: make(chan struct{}, 1)}
}

// Wake tells the workers there's something new in the queue, or that the
// settings changed.
func (p *Processor) Wake() {
	poke(p.readWake)
	poke(p.suggestWake)
}

func poke(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// Limits are how many documents are read at once and how many suggestions
// are asked for at once: the settings, else the defaults.
func (p *Processor) Limits(set store.Settings) (read, suggest int) {
	read, suggest = set.OCRWorkers, set.SuggestWorkers
	if read < 1 {
		read = p.Workers
	}
	if suggest < 1 {
		suggest = DefaultSuggestWorkers
	}
	return min(max(read, 1), MaxOCRWorkers), min(suggest, MaxSuggestWorkers)
}

// Run processes queued documents until ctx is done: one pool of workers
// reads them, another asks for their suggestions, so a long OCR doesn't
// hold up suggestions and suggestions don't wait on each other.
func (p *Processor) Run(ctx context.Context) {
	if err := p.Store.ResetJobs(ctx); err != nil {
		slog.Error("processing: reset the queue", "err", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		p.pool(ctx, p.readWake, func(set store.Settings) int { n, _ := p.Limits(set); return n },
			p.Store.ClaimJob, p.readJob)
	})
	wg.Go(func() {
		p.pool(ctx, p.suggestWake, func(set store.Settings) int { _, n := p.Limits(set); return n },
			p.Store.ClaimSuggestion, p.suggestJob)
	})
	wg.Wait()
}

// pool runs up to limit jobs at once, checking the limit (a setting)
// whenever one finishes or it's woken. Claiming is atomic, so workers never
// get the same document.
func (p *Processor) pool(ctx context.Context, wake chan struct{}, limit func(store.Settings) int,
	claim func(context.Context) (*store.Job, error), do func(context.Context, *store.Job)) {
	var wg sync.WaitGroup
	defer wg.Wait()
	done := make(chan struct{})
	running := 0
	for {
		set, _ := p.Store.Settings(ctx) // the defaults when it fails
		for n := limit(set); running < n && ctx.Err() == nil; {
			job, err := claim(ctx)
			if errors.Is(err, store.ErrNotFound) {
				break
			}
			if err != nil {
				if ctx.Err() == nil {
					slog.Error("processing: next job", "err", err)
				}
				break
			}
			running++
			wg.Go(func() {
				do(ctx, job)
				select {
				case done <- struct{}{}:
				case <-ctx.Done():
				}
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-done:
			running--
		case <-wake:
		case <-time.After(time.Minute):
		}
	}
}

// readJob makes a document's PDF, thumbnail and text, then hands it to the
// suggestion workers when it wants a suggestion.
func (p *Processor) readJob(ctx context.Context, job *store.Job) {
	start := time.Now()
	res := p.read(ctx, job)
	if ctx.Err() != nil {
		return // shutting down: ResetJobs queues it again next time
	}
	var err error
	// Even a file that couldn't be read can be classified from its name.
	suggest := p.wantsSuggestion(ctx, job)
	if suggest {
		err = p.Store.ReadDone(ctx, job.ID, res)
		poke(p.suggestWake)
	} else {
		err = p.Store.FinishJob(ctx, job.ID, res)
	}
	if err != nil {
		slog.Error("processing: save", "document", job.ID, "err", err)
	}
	slog.Info("read", "document", job.ID, "pages", res.Pages, "text", res.TextSource,
		"took", time.Since(start).Round(time.Millisecond), "err", res.Error, "suggestion", suggest)
}

// suggestJob asks the classifier about a document that was read.
func (p *Processor) suggestJob(ctx context.Context, job *store.Job) {
	if job.Error == "" && !p.cached(job) {
		// Read before, but its thumbnail or PDF is gone: read it again.
		if err := p.Store.ReadAgain(ctx, job.ID); err != nil {
			slog.Error("processing: requeue", "document", job.ID, "err", err)
		}
		poke(p.readWake)
		return
	}
	start := time.Now()
	res := store.JobResult{Pages: job.Pages, Text: job.Text, TextSource: job.TextSource, OCRLang: job.OCRLang, Error: job.Error}
	if p.Classifier != nil {
		p.classify(ctx, job, &res)
	}
	if ctx.Err() != nil {
		return
	}
	if err := p.Store.FinishSuggestion(ctx, job.ID, res); err != nil {
		slog.Error("processing: save", "document", job.ID, "err", err)
	}
	slog.Info("suggested", "document", job.ID, "took", time.Since(start).Round(time.Millisecond),
		"err", res.ClassifyError)
}

// CachePDF is the PDF made from a photo document.
func (p *Processor) CachePDF(id int64) string {
	return filepath.Join(p.Cache, strconv.FormatInt(id, 10), "doc.pdf")
}

// Thumb is a document's page-one thumbnail.
func (p *Processor) Thumb(id int64) string {
	return filepath.Join(p.Cache, strconv.FormatInt(id, 10), "thumb.jpg")
}

func IsImage(mime string) bool { return strings.HasPrefix(mime, "image/") }

// cached says the derived files of a document that was read are there:
// its thumbnail, and a photo's PDF.
func (p *Processor) cached(job *store.Job) bool {
	if _, err := os.Stat(p.Thumb(job.ID)); err != nil {
		return false
	}
	if IsImage(job.Mime) {
		if _, err := os.Stat(p.CachePDF(job.ID)); err != nil {
			return false
		}
	}
	return true
}

// read makes the PDF, thumbnail and text.
func (p *Processor) read(ctx context.Context, job *store.Job) store.JobResult {
	var res store.JobResult
	dir := filepath.Join(p.Cache, strconv.FormatInt(job.ID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		res.Error = err.Error()
		return res
	}
	tmp, err := os.MkdirTemp(dir, "work-")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer os.RemoveAll(tmp)

	src := filepath.Join(p.Files, job.FilePath)
	pdf := src
	if IsImage(job.Mime) {
		pdf = p.CachePDF(job.ID)
		if err := imageToPDF(ctx, src, job.Mime, pdf, tmp); err != nil {
			res.Error = "couldn't convert the image: " + err.Error()
			return res
		}
	}

	pages, err := pageCount(ctx, pdf)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Pages = pages

	thumbTmp := filepath.Join(tmp, "thumb")
	if _, err := run(ctx, "pdftoppm", "-f", "1", "-l", "1", "-scale-to", "480", "-jpeg", "-jpegopt", "quality=80",
		"-singlefile", pdf, thumbTmp); err != nil {
		res.Error = "thumbnail: " + err.Error()
	} else if err := os.Rename(thumbTmp+".jpg", p.Thumb(job.ID)); err != nil {
		res.Error = "thumbnail: " + err.Error()
	}

	if !job.ForceOCR && job.TextSource == "ocr" && job.Text != "" {
		// OCR'd before: keep that text (Run OCR again forces a new one).
		res.Text, res.TextSource, res.OCRLang = job.Text, "ocr", job.OCRLang
	} else if !job.ForceOCR {
		out, err := run(ctx, "pdftotext", "-enc", "UTF-8", "-layout", pdf, "-")
		if err != nil {
			res.Error = join(res.Error, "text: "+err.Error())
		} else if hasText(out) {
			res.Text, res.TextSource = clip(out), "pdf"
		}
	}
	if res.TextSource == "" {
		lang := job.OCRLang
		if lang == "" {
			set, _ := p.Store.Settings(ctx)
			lang = set.OCRLangs
		}
		text, err := ocr(ctx, pdf, pages, lang, tmp)
		if err != nil {
			res.Error = join(res.Error, "OCR: "+err.Error())
		} else {
			res.Text, res.TextSource, res.OCRLang = clip(text), "ocr", lang
		}
	}
	return res
}

// wantsSuggestion: suggestions are on, and someone asked for one or every
// document gets one.
func (p *Processor) wantsSuggestion(ctx context.Context, job *store.Job) bool {
	if p.Classifier == nil || ctx.Err() != nil {
		return false
	}
	if job.WantSuggestion {
		return true
	}
	set, err := p.Store.Settings(ctx)
	return err == nil && set.SuggestNew == "auto"
}

func (p *Processor) classify(ctx context.Context, job *store.Job, res *store.JobResult) {
	in, set, err := buildInput(ctx, p.Store, job, res.Text)
	if err != nil {
		res.ClassifyError = err.Error()
		return
	}
	people := set.People
	// Send only the title when that's enough: less of the document leaves.
	text := in.Text
	titleOnly := set.ClassifyFrom == "title" || (set.ClassifyFrom != "text" && Descriptive(job.Title))
	if titleOnly {
		in.Text = ""
	}
	sg, err := p.Classifier.Classify(ctx, in)
	if err == nil && titleOnly && set.ClassifyFrom != "title" && text != "" {
		// Auto: the title wasn't enough to pick a category, so try the text.
		if s := Sanitize(*sg, in, people); s == nil || s.Category == "" {
			in.Text = text
			sg, err = p.Classifier.Classify(ctx, in)
		}
	}
	res.ClassifierInput = in.InputText()
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("classifier", "document", job.ID, "err", err)
			res.ClassifyError = err.Error()
		}
		return
	}
	res.Classified = true
	res.Suggestion = Sanitize(*sg, in, people)
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxText {
		s = strings.ToValidUTF8(s[:maxText], "")
	}
	return s
}

// hasText says whether a PDF's text layer is real text rather than the
// odd stray character of a scan without one.
func hasText(s string) bool {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if n++; n >= 20 {
				return true
			}
		}
	}
	return false
}

func imageToPDF(ctx context.Context, src, mime, out, tmp string) error {
	switch mime {
	case "image/jpeg":
		return jpegToPDF(src, out)
	case "image/png":
		return pngToPDF(src, out, filepath.Join(tmp, "image.jpg"))
	case "image/heic", "image/heif":
		jpg := filepath.Join(tmp, "image.jpg")
		// heif-dec applies the image's rotation itself.
		if _, err := run(ctx, "heif-dec", "--quality", "90", src, jpg); err != nil {
			return err
		}
		return jpegToPDF(jpg, out)
	}
	return fmt.Errorf("unsupported image type %s", mime)
}

func pageCount(ctx context.Context, pdf string) (int, error) {
	out, err := run(ctx, "pdfinfo", pdf)
	if err != nil {
		if strings.Contains(err.Error(), "Incorrect password") {
			return 0, errors.New("the PDF is password-protected, so there's no thumbnail or text (it still opens with its password)")
		}
		return 0, fmt.Errorf("not a readable PDF: %w", err)
	}
	for line := range strings.Lines(out) {
		if v, ok := strings.CutPrefix(line, "Pages:"); ok {
			return strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return 0, errors.New("not a readable PDF: no page count")
}

// ocrDPI and ocrMaxPixels: pages are rendered at 300 dpi, but no longer
// than 4200 px. Photo PDFs often say their pages are a metre wide (images
// at 72 dpi), and at 300 dpi Tesseract runs out of memory on those.
const (
	ocrDPI       = 300
	ocrMaxPixels = 4200
)

// pageDPI is the resolution to render a page at, from its size in points.
func pageDPI(ctx context.Context, pdf string, page int) int {
	n := strconv.Itoa(page)
	out, err := run(ctx, "pdfinfo", "-f", n, "-l", n, pdf)
	if err != nil {
		return ocrDPI
	}
	for line := range strings.Lines(out) {
		_, size, ok := strings.Cut(line, " size: ")
		if !ok || !strings.HasPrefix(line, "Page") {
			continue
		}
		var w, h float64
		if _, err := fmt.Sscanf(size, "%g x %g", &w, &h); err != nil || max(w, h) <= 0 {
			break
		}
		return max(50, min(ocrDPI, int(ocrMaxPixels*72/max(w, h))))
	}
	return ocrDPI
}

// ocr renders each page (see pageDPI) and runs Tesseract on it, one page at
// a time, so only one page image is ever on disk or in memory.
func ocr(ctx context.Context, pdf string, pages int, lang, tmp string) (string, error) {
	var text strings.Builder
	for page := 1; page <= min(pages, maxOCRPages); page++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		img := filepath.Join(tmp, "page")
		n := strconv.Itoa(page)
		if _, err := run(ctx, "pdftoppm", "-f", n, "-l", n, "-r", strconv.Itoa(pageDPI(ctx, pdf, page)), "-gray", "-png", "-singlefile", pdf, img); err != nil {
			return "", fmt.Errorf("page %d: %w", page, err)
		}
		out, err := run(ctx, "tesseract", img+".png", "stdout", "-l", lang, "--psm", "3")
		os.Remove(img + ".png")
		if err != nil {
			return "", fmt.Errorf("page %d: %w", page, err)
		}
		if page > 1 {
			text.WriteString("\f")
		}
		text.WriteString(out)
	}
	return text.String(), nil
}

var lowPriority = sync.OnceValue(func() bool {
	_, err := exec.LookPath("nice")
	return err == nil
})

// run runs a tool at low priority and returns its output; the error
// carries the tool's own message.
func run(ctx context.Context, name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", fmt.Errorf("%s isn't installed", name)
	}
	ctx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()
	if lowPriority() {
		args = append([]string{"-n", "15", name}, args...)
		name = "nice"
	}
	cmd := exec.CommandContext(ctx, name, args...)
	// Tesseract's own threads only add memory here: pages go one by one.
	cmd.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

var langs = sync.OnceValue(func() []string {
	out, err := run(context.Background(), "tesseract", "--list-langs")
	if err != nil {
		return nil
	}
	var list []string
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, " ") || line == "osd" {
			continue
		}
		list = append(list, line)
	}
	return list
})

// Languages are the OCR languages installed.
func Languages() []string { return langs() }
