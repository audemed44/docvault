package process

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/audemed44/docvault/internal/store"
)

// Classifier asks an outside service (DOCVAULT_CLASSIFIER_URL) to suggest
// a title, category, tags and dates for a document from its text. The
// contract is in the README: Docvault POSTs ClassifyRequest and reads a
// store.Suggest back. Suggestions are only shown, never applied by
// themselves. Whatever the URL points at sees the document's text, so a
// local model keeps everything on the server.
type Classifier struct {
	URL   string
	Token string // sent as a bearer token when set
	HTTP  *http.Client
}

// maxClassifyText is how much of the text the classifier gets.
const maxClassifyText = 24 << 10

type ClassifyRequest struct {
	DocumentID int64    `json:"document_id"`
	Title      string   `json:"title"`
	Text       string   `json:"text"`
	Categories []string `json:"categories"`
	Tags       []string `json:"tags"` // tags already in use, to prefer
}

func NewClassifier(url, token string) *Classifier {
	return &Classifier{URL: url, Token: token, HTTP: &http.Client{Timeout: 2 * time.Minute}}
}

func (c *Classifier) Classify(ctx context.Context, st *store.Store, job *store.Job, text string) (*store.Suggest, error) {
	cats, err := st.Categories(ctx, 0)
	if err != nil {
		return nil, err
	}
	tags, err := st.TagNames(ctx)
	if err != nil {
		return nil, err
	}
	req := ClassifyRequest{DocumentID: job.ID, Title: job.Title, Text: text, Tags: tags}
	if len(req.Text) > maxClassifyText {
		req.Text = strings.ToValidUTF8(req.Text[:maxClassifyText], "")
	}
	for _, c := range cats {
		req.Categories = append(req.Categories, c.Name)
	}
	body, _ := json.Marshal(req)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(hreq)
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
	return Sanitize(sg, req.Categories), nil
}

// Sanitize keeps what's usable from a suggestion: a known category, valid
// dates, and a few short tags.
func Sanitize(sg store.Suggest, categories []string) *store.Suggest {
	out := &store.Suggest{Title: strings.TrimSpace(sg.Title)}
	if len(out.Title) > 200 {
		out.Title = ""
	}
	if i := slices.IndexFunc(categories, func(c string) bool { return strings.EqualFold(c, strings.TrimSpace(sg.Category)) }); i >= 0 {
		out.Category = categories[i]
	}
	for _, t := range sg.Tags {
		t = strings.TrimSpace(t)
		if t != "" && len(t) <= 40 && len(out.Tags) < 8 {
			out.Tags = append(out.Tags, t)
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
