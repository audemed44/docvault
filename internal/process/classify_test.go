package process

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/audemed44/docvault/internal/mask"
	"github.com/audemed44/docvault/internal/store"
)

func TestSanitize(t *testing.T) {
	in := Input{
		Categories: []CategoryTags{{Name: "ID", Tags: []string{"pan"}}, {Name: "Medical"}},
		Tags:       []string{"pan", "lab-report", "[person:1]"},
	}
	people := []mask.Person{{Name: "Pranav"}}
	sg := Sanitize(store.Suggest{Title: "PAN card - [person:1]", Category: "id",
		Tags: []string{"PAN", "made-up", "[person:1]", "pan", "[person:2]"}, DocDate: "15/03/2024", Expires: "2030-01-01"}, in, people)
	if sg.Title != "PAN card - Pranav" || sg.Category != "ID" || strings.Join(sg.Tags, ",") != "pan,Pranav" ||
		sg.DocDate != "" || sg.Expires != "2030-01-01" {
		t.Fatalf("%+v", sg)
	}
	if sg := Sanitize(store.Suggest{Title: "Policy [number]", Category: "Recipes"}, in, people); sg != nil {
		t.Fatalf("kept %+v", sg)
	}
}

// fakeModel is an OpenAI-compatible chat API that records what it's sent.
type fakeModel struct {
	srv      *httptest.Server
	requests []chatRequest
	fail     atomic.Int32 // answer 429 this many times first
	answer   string
}

func newFakeModel(t *testing.T, answer string) *fakeModel {
	f := &fakeModel{answer: answer}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"error":{"message":"bad request"}}`, 400)
			return
		}
		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.requests = append(f.requests, req)
		if f.fail.Add(-1) >= 0 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"error":{"message":"slow down"}}`, 429)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]string{"role": "assistant", "content": f.answer}}}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestLLMClassify(t *testing.T) {
	p, st, u := setup(t)
	ctx := context.Background()
	st.SaveSettings(ctx, store.Settings{OCRLangs: "eng", People: []mask.Person{{Name: "Pranav", Aliases: []string{"Pranav Kumar Sample"}}},
		MaskWords: []string{"Sample"}})
	fake := newFakeModel(t, "```json\n"+`{"title":"PAN card - [person:1]","category":"ID","tags":["pan","[person:1]","invented"],"doc_date":"","expires":""}`+"\n```")
	fake.fail.Store(1)
	llm := NewLLM(fake.srv.URL+"/v1", "test-key", "test/model")
	llm.retryWait = 0
	p.Classifier = llm

	d := &store.Document{OwnerID: u.ID, Title: "CamScanner 03-15-2021", DocDate: "2021-03-15", FileName: "x.pdf", Mime: "application/pdf", SHA256: "a"}
	st.CreateDocument(ctx, d, u.ID)
	job, _ := st.ClaimJob(ctx)
	res := store.JobResult{Pages: 1, Text: "INCOME TAX DEPARTMENT\nPranav Kumar Sample\nPermanent Account Number ABCPS1234K", TextSource: "pdf"}
	p.classify(ctx, job, &res)
	if res.ClassifyError != "" || !res.Classified {
		t.Fatalf("classify: %+v", res)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("expected a retry after 429, got %d requests", len(fake.requests))
	}
	req := fake.requests[1]
	sent := req.Messages[1].Content
	for _, leak := range []string{"ABCPS1234K", "Pranav", "Sample"} {
		if strings.Contains(sent, leak) {
			t.Fatalf("%q sent to the model:\n%s", leak, sent)
		}
	}
	if !strings.Contains(sent, "[pan]") || !strings.Contains(sent, "Family members: [person:1]") ||
		!strings.Contains(sent, "- ID: aadhaar, pan, passport") ||
		!strings.Contains(sent, "- Banking & Investments: fd, loan, cheque, account-opening, mutual-fund") ||
		!strings.Contains(sent, "- Travel: airline-ticket") || strings.Contains(sent, "- Other") {
		t.Fatalf("prompt:\n%s", sent)
	}
	if req.Provider != nil {
		t.Fatal("OpenRouter routing sent to another API")
	}
	schemaJSON, _ := json.Marshal(req.ResponseFormat)
	if !strings.Contains(string(schemaJSON), `"enum":["aadhaar","pan"`) {
		t.Fatalf("schema %s", schemaJSON)
	}
	if res.Suggestion.Title != "PAN card - Pranav" || strings.Join(res.Suggestion.Tags, ",") != "pan,Pranav" {
		t.Fatalf("suggestion %+v", res.Suggestion)
	}
	if !strings.Contains(res.ClassifierInput, "[pan]") || strings.Contains(res.ClassifierInput, "ABCPS1234K") {
		t.Fatalf("input kept %q", res.ClassifierInput)
	}

	// Saved with the document, and shown as what was sent.
	st.FinishJob(ctx, d.ID, res)
	got, _ := st.Document(ctx, u.ID, d.ID)
	if got.Classified.IsZero() || got.Suggestion == nil || got.Suggestion.Category != "ID" {
		t.Fatalf("saved %+v", got)
	}
	if in, _ := st.ClassifierInput(ctx, u.ID, d.ID); in != res.ClassifierInput {
		t.Fatalf("input %q", in)
	}
}

func TestLLMErrors(t *testing.T) {
	ctx := context.Background()
	fake := newFakeModel(t, "not json at all")
	llm := NewLLM(fake.srv.URL+"/v1", "test-key", "m")
	if _, err := llm.Classify(ctx, Input{}); err == nil || !strings.Contains(err.Error(), "isn't JSON") {
		t.Fatalf("bad answer: %v", err)
	}
	bad := NewLLM(fake.srv.URL+"/v1", "wrong-key", "m")
	if _, err := bad.Classify(ctx, Input{}); err == nil || !strings.Contains(err.Error(), "400") || len(fake.requests) != 1 {
		t.Fatalf("no retry on 400: %v, %d requests", err, len(fake.requests))
	}
	if !NewLLM("", "k", "m").openRouter() {
		t.Fatal("OpenRouter is the default")
	}
}

func TestOCRTextKept(t *testing.T) {
	need(t, "pdfinfo", "pdftoppm", "pdftotext")
	p, st, u := setup(t)
	ctx := context.Background()
	d := addFile(t, p, st, u, "scan.pdf", "application/pdf", textPDF("Short"))
	// Pretend it was OCR'd: asking again without forcing OCR keeps the text.
	st.ClaimJob(ctx) // nothing queued
	st.Requeue(ctx, u.ID, d.ID, true, "eng", false)
	job, _ := st.ClaimJob(ctx)
	st.FinishJob(ctx, job.ID, store.JobResult{Pages: 1, Text: "earlier OCR text with enough letters to count", TextSource: "ocr", OCRLang: "eng"})
	st.Requeue(ctx, u.ID, d.ID, false, "", false)
	job, _ = st.ClaimJob(ctx)
	if job.TextSource != "ocr" || job.OCRLang != "eng" || !strings.HasPrefix(job.Text, "earlier") {
		t.Fatalf("job %+v", job)
	}
	res := p.process(ctx, job)
	if res.TextSource != "ocr" || !strings.HasPrefix(res.Text, "earlier") {
		t.Fatalf("result %+v", res)
	}
}

func TestDescriptive(t *testing.T) {
	for title, want := range map[string]bool{
		"CamScanner 03-15-2021 10.22":       false,
		"IMG_2041":                          false,
		"Scan 2024-01-09":                   false,
		"Scanned Document (3)":              false,
		"WhatsApp Image 2023-05-01 at 9.10": false,
		"2021-03-15":                        false,
		"Dad passport 2019":                 true,
		"PAN card":                          true,
		"Car insurance renewal":             true,
		"LIC":                               true,
	} {
		if got := Descriptive(title); got != want {
			t.Errorf("Descriptive(%q) = %v", title, got)
		}
	}
}

func TestClassifyFrom(t *testing.T) {
	ctx := context.Background()
	withCategory := `{"title":"","category":"ID","tags":[],"doc_date":"","expires":""}`
	noCategory := `{"title":"","category":"","tags":[],"doc_date":"","expires":""}`
	for _, c := range []struct {
		name, mode, title, answer string
		requests                  int
		textSent                  bool
	}{
		{"auto, good title", "auto", "Dad passport 2019", withCategory, 1, false},
		{"auto, title not enough", "auto", "Dad docs", noCategory, 2, true},
		{"auto, scanner name", "auto", "CamScanner 03-15-2021 10.22", withCategory, 1, true},
		{"title only", "title", "CamScanner 03-15-2021 10.22", noCategory, 1, false},
		{"always text", "text", "Dad passport 2019", withCategory, 1, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, st, u := setup(t)
			st.SaveSettings(ctx, store.Settings{OCRLangs: "eng", ClassifyFrom: c.mode})
			fake := newFakeModel(t, c.answer)
			p.Classifier = NewLLM(fake.srv.URL+"/v1", "test-key", "m")
			d := &store.Document{OwnerID: u.ID, Title: c.title, DocDate: "2024-01-01", FileName: "x.pdf", Mime: "application/pdf", SHA256: "a"}
			st.CreateDocument(ctx, d, u.ID)
			job, _ := st.ClaimJob(ctx)
			res := store.JobResult{Text: "REPUBLIC OF INDIA passport", TextSource: "pdf"}
			p.classify(ctx, job, &res)
			if len(fake.requests) != c.requests {
				t.Fatalf("%d requests", len(fake.requests))
			}
			last := fake.requests[len(fake.requests)-1].Messages[1].Content
			if sent := strings.Contains(last, "REPUBLIC OF INDIA"); sent != c.textSent {
				t.Fatalf("text sent: %v\n%s", sent, last)
			}
			if first := fake.requests[0].Messages[1].Content; c.requests == 2 && strings.Contains(first, "REPUBLIC") {
				t.Fatal("the first try sent the text")
			}
			if strings.Contains(res.ClassifierInput, "REPUBLIC") != c.textSent {
				t.Fatalf("what it saw: %q", res.ClassifierInput)
			}
		})
	}
}

func TestWantsSuggestion(t *testing.T) {
	p, st, u := setup(t)
	ctx := context.Background()
	d := &store.Document{OwnerID: u.ID, Title: "x", DocDate: "2024-01-01", FileName: "x.pdf", Mime: "application/pdf", SHA256: "a"}
	st.CreateDocument(ctx, d, u.ID)
	job, _ := st.ClaimJob(ctx)
	if p.wantsSuggestion(ctx, job) {
		t.Fatal("suggestions are off")
	}
	p.Classifier = NewHook("http://127.0.0.1:1", "")
	if p.wantsSuggestion(ctx, job) {
		t.Fatal("asked without anyone asking (default: ask)")
	}
	st.FinishJob(ctx, job.ID, store.JobResult{})
	st.Requeue(ctx, u.ID, d.ID, false, "", true)
	job, _ = st.ClaimJob(ctx)
	if !job.WantSuggestion || !p.wantsSuggestion(ctx, job) {
		t.Fatal("asked for, but not wanted")
	}
	st.FinishJob(ctx, job.ID, store.JobResult{})
	st.Requeue(ctx, u.ID, d.ID, false, "", false)
	job, _ = st.ClaimJob(ctx)
	if job.WantSuggestion {
		t.Fatal("the request wasn't cleared")
	}
	st.SaveSettings(ctx, store.Settings{SuggestNew: "auto"})
	if !p.wantsSuggestion(ctx, job) {
		t.Fatal("auto")
	}
}

func TestNewTags(t *testing.T) {
	in := Input{Categories: []CategoryTags{{Name: "Bills"}}, Tags: []string{"invoice", "[person:1]"}}
	sg := Sanitize(store.Suggest{Category: "Bills", NewTags: []string{"Airline Ticket", "INVOICE", "[person:1]", "x", "travel", "third"}},
		in, []mask.Person{{Name: "Pranav"}})
	if strings.Join(sg.NewTags, ",") != "airline-ticket,travel" || strings.Join(sg.Tags, ",") != "invoice" {
		t.Fatalf("%+v", sg)
	}
	if s := Sanitize(store.Suggest{NewTags: []string{"boarding-pass"}}, in, nil); s == nil || s.NewTags[0] != "boarding-pass" {
		t.Fatalf("new tags alone: %+v", s)
	}
}

func TestUsedTagsInPrompt(t *testing.T) {
	p, st, u := setup(t)
	ctx := context.Background()
	st.SaveSettings(ctx, store.Settings{People: []mask.Person{{Name: "Pranav"}}})
	// Tags in use: one on the list, a person, and one that isn't listed.
	d := &store.Document{OwnerID: u.ID, Title: "a", DocDate: "2024-01-01", FileName: "a.pdf", Mime: "application/pdf", SHA256: "a",
		Tags: []string{"pan", "Pranav", "bank-locker"}}
	st.CreateDocument(ctx, d, u.ID)
	fake := newFakeModel(t, `{"title":"","category":"","tags":[],"new_tags":["Boarding Pass"],"doc_date":"","expires":""}`)
	p.Classifier = NewLLM(fake.srv.URL+"/v1", "test-key", "m")
	job, _ := st.ClaimJob(ctx)
	res := store.JobResult{Text: "INDIGO boarding pass", TextSource: "pdf"}
	p.classify(ctx, job, &res)
	sent := fake.requests[0].Messages[1].Content
	if !strings.Contains(sent, "Other tags already in use (for new_tags): bank-locker\n") {
		t.Fatalf("prompt:\n%s", sent)
	}
	schemaJSON, _ := json.Marshal(fake.requests[0].ResponseFormat)
	if !strings.Contains(string(schemaJSON), `"new_tags"`) {
		t.Fatal("schema has no new_tags")
	}
	if res.Suggestion == nil || res.Suggestion.NewTags[0] != "boarding-pass" {
		t.Fatalf("suggestion %+v", res.Suggestion)
	}
}

func TestOtherNeverSuggested(t *testing.T) {
	p, st, u := setup(t)
	ctx := context.Background()
	fake := newFakeModel(t, `{"title":"","category":"Other","tags":[],"new_tags":[],"doc_date":"","expires":""}`)
	p.Classifier = NewLLM(fake.srv.URL+"/v1", "test-key", "m")
	d := &store.Document{OwnerID: u.ID, Title: "Misc papers", DocDate: "2024-01-01", FileName: "x.pdf", Mime: "application/pdf", SHA256: "a"}
	st.CreateDocument(ctx, d, u.ID)
	job, _ := st.ClaimJob(ctx)
	res := store.JobResult{Text: "something", TextSource: "pdf"}
	p.classify(ctx, job, &res)
	schemaJSON, _ := json.Marshal(fake.requests[len(fake.requests)-1].ResponseFormat)
	if strings.Contains(string(schemaJSON), `"Other"`) {
		t.Fatalf("Other offered: %s", schemaJSON)
	}
	if res.Suggestion != nil && res.Suggestion.Category != "" {
		t.Fatalf("suggested %q", res.Suggestion.Category)
	}
}
