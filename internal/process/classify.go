package process

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/audemed44/docvault/internal/mask"
	"github.com/audemed44/docvault/internal/store"
)

// A Classifier suggests a title, category, tags and dates for a document.
// It only ever sees masked text (package mask), and only picks from the
// categories and tags it's given; Sanitize drops anything else. Its
// suggestions are shown on the document, never applied by themselves.
type Classifier interface {
	Classify(ctx context.Context, in Input) (*store.Suggest, error)
	// Name says what's on, for the settings page.
	Name() string
}

// Input is everything a classifier gets.
type Input struct {
	DocumentID int64  `json:"document_id"`
	Title      string `json:"title"` // masked
	Text       string `json:"text"`  // masked, at most maxClassifyText
	// Categories, in order, with the tags usually filed under each.
	Categories []CategoryTags `json:"categories"`
	// Tags are every tag it may suggest: the vocabulary, and the people as
	// their placeholders (turned back into names here).
	Tags []string `json:"tags"`
	// People are the family's placeholders, "[person:1]" and on: the names
	// themselves are never sent.
	People []string `json:"people"`
	// UsedTags are tags already on documents but not on the list, to reuse
	// when proposing new ones.
	UsedTags []string `json:"used_tags"`
}

type CategoryTags struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// maxClassifyText is how much of the text is sent: the start of a
// document says what it is.
const maxClassifyText = 12 << 10

// buildInput masks the document and gathers the choices.
func buildInput(ctx context.Context, st *store.Store, job *store.Job, text string) (Input, store.Settings, error) {
	set, err := st.Settings(ctx)
	if err != nil {
		return Input{}, set, err
	}
	cats, err := st.Categories(ctx, 0)
	if err != nil {
		return Input{}, set, err
	}
	vocab, err := st.TagVocab(ctx)
	if err != nil {
		return Input{}, set, err
	}
	opts := mask.Options{People: set.People, Words: set.MaskWords}
	if len(text) > maxClassifyText {
		text = strings.ToValidUTF8(text[:maxClassifyText], "")
	}
	in := Input{DocumentID: job.ID, Title: mask.Title(job.Title, opts), Text: mask.Text(text, opts),
		Categories: []CategoryTags{}, Tags: []string{}, People: []string{}, UsedTags: []string{}}
	byCat := map[int64][]string{}
	for _, t := range vocab {
		byCat[t.CategoryID] = append(byCat[t.CategoryID], t.Name)
		in.Tags = append(in.Tags, t.Name)
	}
	for _, c := range cats {
		if strings.EqualFold(c.Name, OtherCategory) {
			// Never suggested: unsure means it stays in the Inbox, to sort.
			byCat[0] = append(byCat[0], byCat[c.ID]...)
			continue
		}
		in.Categories = append(in.Categories, CategoryTags{Name: c.Name, Tags: append([]string{}, byCat[c.ID]...)})
	}
	if extra := byCat[0]; len(extra) > 0 {
		in.Categories = append(in.Categories, CategoryTags{Name: "", Tags: extra})
	}
	for i := range set.People {
		in.People = append(in.People, mask.Placeholder(i))
		in.Tags = append(in.Tags, mask.Placeholder(i))
	}
	used, err := st.UsedTags(ctx, job.OwnerID)
	if err != nil {
		return Input{}, set, err
	}
	for _, t := range used {
		listed := slices.ContainsFunc(in.Tags, func(v string) bool { return strings.EqualFold(v, t) }) ||
			slices.ContainsFunc(set.People, func(p mask.Person) bool { return strings.EqualFold(p.Name, t) })
		if !listed && len(in.UsedTags) < 100 {
			in.UsedTags = append(in.UsedTags, t)
		}
	}
	return in, set, nil
}

// OtherCategory is never suggested: a document the model can't place stays
// in the Inbox, where it gets looked at, instead of disappearing into Other.
const OtherCategory = "Other"

// InputText is how the input is shown under "What the classifier saw".
func (in Input) InputText() string {
	if in.Text == "" {
		return "Title: " + in.Title + "\n\n(The text wasn't sent.)"
	}
	return "Title: " + in.Title + "\n\n" + in.Text
}

var (
	// genericWords are what scanner apps and cameras name files.
	genericWords = regexp.MustCompile(`(?i)\b(camscanner|scanned|scan|document|doc|new|untitled|img|image|photo|pxl|dsc|dscn|whatsapp|screenshot|file|pdf|page|copy|final|at|am|pm)\b`)
	letters      = regexp.MustCompile(`\pL{2,}`)
)

// Descriptive says whether a title says what the document is ("Dad
// passport 2019") rather than being a scanner's default name
// ("CamScanner 03-15-2021 10.22", "IMG_2041", "Scan 2024-01-09").
func Descriptive(title string) bool {
	rest := genericWords.ReplaceAllString(strings.ReplaceAll(title, "_", " "), " ")
	n := 0
	for _, w := range letters.FindAllString(rest, -1) {
		n += len([]rune(w))
	}
	return n >= 3
}

var (
	personRe = regexp.MustCompile(`^\[person:\d+\]$`)
	// A person in a title with what joins them to it: "Aadhaar card -
	// [person:1]", "[person:1]'s PAN card", "Letter for [person:2]".
	titlePersonRe = regexp.MustCompile(`(?i)(\s*(?:[-–—,:]|\bof\b|\bfor\b|\bto\b))?\s*(\[person:\d+\])(?:'s\b)?`)
)

// dropPeople takes the people drop says so out of a title.
func dropPeople(title string, drop func(string) bool) string {
	out := titlePersonRe.ReplaceAllStringFunc(title, func(m string) string {
		if drop(titlePersonRe.FindStringSubmatch(m)[2]) {
			return ""
		}
		return m
	})
	return strings.Trim(strings.Join(strings.Fields(out), " "), " -–—,:")
}

// Sanitize keeps what's usable from a suggestion: a known category, known
// tags, valid dates, and a title without placeholders (people's are turned
// back into their names).
func Sanitize(sg store.Suggest, in Input, people []mask.Person) *store.Suggest {
	out := &store.Suggest{}
	// A family member only counts when they're in what was sent: unsure
	// models fill in [person:1] (or whoever) rather than leave them out.
	sent := in.Title + "\n" + in.Text
	absent := func(p string) bool { return personRe.MatchString(p) && !strings.Contains(sent, p) }
	if title, left := mask.Unmask(dropPeople(strings.TrimSpace(sg.Title), absent), people); !left && len(title) <= 200 {
		out.Title = title
	}
	for _, c := range in.Categories {
		if c.Name != "" && strings.EqualFold(c.Name, strings.TrimSpace(sg.Category)) {
			out.Category = c.Name
		}
	}
	for _, t := range sg.Tags {
		i := slices.IndexFunc(in.Tags, func(v string) bool { return strings.EqualFold(v, strings.TrimSpace(t)) })
		if i < 0 || absent(in.Tags[i]) {
			continue
		}
		tag, left := mask.Unmask(in.Tags[i], people) // a person's placeholder: their name
		if !left && !slices.Contains(out.Tags, tag) && len(out.Tags) < 8 {
			out.Tags = append(out.Tags, tag)
		}
	}
	for _, t := range sg.NewTags {
		t = newTag(t)
		switch {
		case t == "" || len(out.NewTags) >= 2:
		case slices.ContainsFunc(in.Tags, func(v string) bool { return strings.EqualFold(v, t) }):
			// on the list after all: an ordinary tag (people are never new)
			if !strings.HasPrefix(t, "[") && !slices.Contains(out.Tags, t) && len(out.Tags) < 8 {
				i := slices.IndexFunc(in.Tags, func(v string) bool { return strings.EqualFold(v, t) })
				out.Tags = append(out.Tags, in.Tags[i])
			}
		case !slices.Contains(out.NewTags, t):
			out.NewTags = append(out.NewTags, t)
		}
	}
	if _, err := time.Parse(time.DateOnly, sg.DocDate); err == nil {
		out.DocDate = sg.DocDate
	}
	if _, err := time.Parse(time.DateOnly, sg.Expires); err == nil {
		out.Expires = sg.Expires
	}
	if out.Empty() {
		return nil
	}
	return out
}

var notTagChars = regexp.MustCompile(`[^a-z0-9]+`)

// newTag tidies a proposed tag into lowercase words joined by hyphens
// ("Airline Ticket" → "airline-ticket"); "" when it isn't usable.
func newTag(t string) string {
	if strings.Contains(t, "[") { // a placeholder: never a new tag
		return ""
	}
	t = strings.Trim(notTagChars.ReplaceAllString(strings.ToLower(t), "-"), "-")
	if len(t) < 2 || len(t) > 30 {
		return ""
	}
	return t
}

// Hook is a classifier behind any URL (DOCVAULT_CLASSIFIER_URL): Docvault
// POSTs the Input as JSON and reads a store.Suggest back.
type Hook struct {
	URL   string
	Token string // sent as a bearer token when set
	HTTP  *http.Client
}

func NewHook(url, token string) *Hook {
	return &Hook{URL: url, Token: token, HTTP: &http.Client{Timeout: 2 * time.Minute}}
}

func (h *Hook) Name() string { return "hook" }

func (h *Hook) Classify(ctx context.Context, in Input) (*store.Suggest, error) {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("classifier answered %s", resp.Status)
	}
	var sg store.Suggest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&sg); err != nil {
		return nil, fmt.Errorf("classifier answer: %w", err)
	}
	return &sg, nil
}
