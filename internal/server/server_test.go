package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/audemed44/docvault/internal/process"
	"github.com/audemed44/docvault/internal/store"
)

const token = "test-token"

func TestMain(m *testing.M) {
	pbkdf2Iterations = 1000
	os.Exit(m.Run())
}

type harness struct {
	t    *testing.T
	h    http.Handler
	s    *Server
	data string
}

func newServer(t *testing.T) *harness {
	t.Helper()
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>app")}}
	proc := process.New(process.Options{Store: db, Files: filepath.Join(data, "files"), Cache: filepath.Join(data, "cache")})
	s, err := New(Options{Store: db, Processor: proc, Token: token, DataDir: data, Web: web})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, h: s.Handler(), s: s, data: data}
}

// do sends a request; auth is a cookie ("docvault_session=…"), a bearer
// token, or "".
func (x *harness) do(method, path, body, auth string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	x.auth(req, auth)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, req)
	return rec
}

func (x *harness) auth(req *http.Request, auth string) {
	switch {
	case strings.HasPrefix(auth, cookieName+"="):
		req.Header.Set("Cookie", auth)
	case auth != "":
		req.Header.Set("Authorization", "Bearer "+auth)
	}
}

func (x *harness) json(rec *httptest.ResponseRecorder, want int, v any) {
	x.t.Helper()
	if rec.Code != want {
		x.t.Fatalf("got %d, want %d: %s", rec.Code, want, rec.Body)
	}
	if v != nil {
		if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
			x.t.Fatal(err)
		}
	}
}

func cookieOf(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.Value != "" {
			return c.Name + "=" + c.Value
		}
	}
	return ""
}

// setup makes the admin "me" and the user "dad", signed in.
func (x *harness) setup() (me, dad string) {
	x.t.Helper()
	rec := x.do("POST", "/api/setup", `{"token":"`+token+`","username":"Me","name":"Aakash","password":"correct horse"}`, "")
	x.json(rec, 200, nil)
	me = cookieOf(rec)
	x.json(x.do("POST", "/api/users", `{"username":"dad","name":"Dad","password":"password1"}`, me), 201, nil)
	rec = x.do("POST", "/api/session", `{"username":"dad","password":"password1"}`, "")
	x.json(rec, 200, nil)
	return me, cookieOf(rec)
}

type part struct {
	field, name string
	data        []byte
}

func (x *harness) upload(auth string, accept string, parts ...part) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range parts {
		if p.name != "" {
			w, _ := mw.CreateFormFile(p.field, p.name)
			w.Write(p.data)
		} else {
			mw.WriteField(p.field, string(p.data))
		}
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/api/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	x.auth(req, auth)
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, req)
	return rec
}

func pdfBytes(text string) []byte {
	return []byte("%PDF-1.4\n% " + text + "\n%%EOF\n")
}

func jpegBytes() []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 8, 8)), nil)
	return b.Bytes()
}

func TestSetupAndSignIn(t *testing.T) {
	x := newServer(t)
	var info sessionInfo
	x.json(x.do("GET", "/api/session", "", ""), 200, &info)
	if info.Authenticated || !info.SetupNeeded {
		t.Fatalf("fresh: %+v", info)
	}
	x.json(x.do("POST", "/api/setup", `{"token":"nope","username":"me","password":"correct horse"}`, ""), 401, nil)
	x.json(x.do("POST", "/api/setup", `{"token":"`+token+`","username":"me","password":"short"}`, ""), 400, nil)
	me, dad := x.setup()
	x.json(x.do("POST", "/api/setup", `{"token":"`+token+`","username":"x","password":"correct horse"}`, ""), 409, nil)

	x.json(x.do("GET", "/api/session", "", me), 200, &info)
	if !info.Authenticated || info.User.Username != "me" || !info.User.Admin {
		t.Fatalf("me: %+v", info)
	}
	x.json(x.do("POST", "/api/session", `{"username":"dad","password":"wrong-password"}`, ""), 401, nil)
	x.json(x.do("POST", "/api/session", `{"username":"nobody","password":"wrong-password"}`, ""), 401, nil)
	x.json(x.do("GET", "/api/users", "", dad), 403, nil)
	x.json(x.do("GET", "/api/documents", "", ""), 401, nil)
	x.json(x.do("GET", "/api/documents", "", token), 401, nil) // DOCVAULT_TOKEN isn't a user

	// Changing the password signs out other sessions.
	x.json(x.do("PUT", "/api/me", `{"name":"Dad","current_password":"wrong","password":"password2"}`, dad), 403, nil)
	rec := x.do("PUT", "/api/me", `{"name":"Papa","current_password":"password1","password":"password2"}`, dad)
	x.json(rec, 200, nil)
	x.json(x.do("GET", "/api/documents", "", dad), 401, nil)
	x.json(x.do("GET", "/api/documents", "", cookieOf(rec)), 200, nil)

	x.json(x.do("DELETE", "/api/session", "", me), 204, nil)
	x.json(x.do("GET", "/api/documents", "", me), 401, nil)
}

func TestTokens(t *testing.T) {
	x := newServer(t)
	_, dad := x.setup()
	var tok struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	x.json(x.do("POST", "/api/tokens", `{"name":"Dad's iPhone"}`, dad), 201, &tok)
	if !strings.HasPrefix(tok.Token, tokenPrefix) {
		t.Fatalf("token %q", tok.Token)
	}
	var list []store.APIToken
	x.json(x.do("GET", "/api/tokens", "", dad), 200, &list)
	if len(list) != 1 || !strings.HasSuffix(tok.Token, list[0].Hint) {
		t.Fatalf("list %+v", list)
	}
	// A token uploads and reads, but can't manage the account.
	x.json(x.do("GET", "/api/categories?format=names", "", tok.Token), 200, nil)
	x.json(x.do("POST", "/api/tokens", `{"name":"another"}`, tok.Token), 403, nil)
	x.json(x.do("PUT", "/api/me", `{"name":"x"}`, tok.Token), 403, nil)
	x.json(x.do("DELETE", "/api/tokens/"+itoa(tok.ID), "", dad), 204, nil)
	x.json(x.do("GET", "/api/documents", "", tok.Token), 401, nil)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestUploadForShortcut(t *testing.T) {
	x := newServer(t)
	me, dad := x.setup()

	rec := x.upload(dad, "", part{"file", "scan.pdf", pdfBytes("one")}, part{"category", "", []byte("medical")},
		part{"title", "", []byte("Blood test")}, part{"tags", "", []byte("lab, 2024")})
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "ok: saved “Blood test”" {
		t.Fatalf("upload: %d %q", rec.Code, rec.Body)
	}
	rec = x.upload(dad, "", part{"file", "again.pdf", pdfBytes("one")})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ok: already saved as “Blood test”") {
		t.Fatalf("duplicate: %d %q", rec.Code, rec.Body)
	}
	rec = x.upload(dad, "", part{"file", "notes.txt", []byte("hello there")})
	if rec.Code != 415 || !strings.HasPrefix(rec.Body.String(), "error: not a PDF") {
		t.Fatalf("text file: %d %q", rec.Code, rec.Body)
	}
	rec = x.upload(dad, "", part{"file", "x.pdf", pdfBytes("two")}, part{"category", "", []byte("Recipes")})
	if !strings.Contains(rec.Body.String(), "unknown category “Recipes”") {
		t.Fatalf("unknown category: %q", rec.Body)
	}

	var list struct {
		Documents []store.Document `json:"documents"`
		Total     int              `json:"total"`
	}
	x.json(x.do("GET", "/api/documents?category=none", "", dad), 200, &list)
	if list.Total != 1 || list.Documents[0].Title != "x" {
		t.Fatalf("inbox %+v", list)
	}
	x.json(x.do("GET", "/api/documents?tag=lab", "", dad), 200, &list)
	d := list.Documents[0]
	if list.Total != 1 || d.Category != "Medical" || d.Status != "pending" || d.AddedBy != "Dad" {
		t.Fatalf("by tag %+v", list)
	}
	// The original is kept byte for byte, and it's dad's alone.
	stored, _ := filepath.Glob(filepath.Join(x.data, "files", itoa(d.ID), "*"))
	if len(stored) != 1 || filepath.Base(stored[0]) != "scan.pdf" {
		t.Fatalf("stored as %v", stored)
	}
	data, _ := os.ReadFile(stored[0])
	if !bytes.Equal(data, pdfBytes("one")) {
		t.Fatal("original changed")
	}
	rec = x.do("GET", "/api/documents/"+itoa(d.ID)+"/file", "", dad)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("file: %d %v", rec.Code, rec.Header())
	}
	x.json(x.do("GET", "/api/documents/"+itoa(d.ID)+"/file", "", me), 404, nil)

	// Moved to Family, I can see it too.
	body := `{"title":"Blood test","category_id":0,"doc_date":"2024-02-01","expires":"","notes":"","tags":["lab"],"family":true}`
	x.json(x.do("PUT", "/api/documents/"+itoa(d.ID), body, dad), 200, nil)
	x.json(x.do("GET", "/api/documents/"+itoa(d.ID), "", me), 200, nil)
	x.json(x.do("PUT", "/api/documents/"+itoa(d.ID), strings.Replace(body, "2024-02-01", "1 Feb", 1), me), 400, nil)

	// Several files at once, as JSON for the web UI.
	rec = x.upload(me, "application/json", part{"file", "a.jpg", jpegBytes()}, part{"file", "CamScanner 03-15-2021 10.22.pdf", pdfBytes("three")},
		part{"space", "", []byte("family")})
	var res struct {
		Message string         `json:"message"`
		Results []ingestResult `json:"results"`
	}
	x.json(rec, 200, &res)
	if res.Message != "ok: saved 2" || res.Results[0].Document.Mime != "image/jpeg" || !res.Results[1].Document.Family ||
		res.Results[1].Document.DocDate != "2021-03-15" || res.Results[1].Document.Title != "CamScanner 03-15-2021 10.22" {
		t.Fatalf("multi: %+v", res)
	}

	x.json(x.do("DELETE", "/api/documents/"+itoa(d.ID), "", me), 204, nil)
	if _, err := os.Stat(filepath.Join(x.data, "files", itoa(d.ID))); !os.IsNotExist(err) {
		t.Fatal("files left behind")
	}
}

func TestImportFolder(t *testing.T) {
	x := newServer(t)
	me, _ := x.setup()
	folder := filepath.Join(x.data, "import", "me", "Receipts")
	os.MkdirAll(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "a.pdf"), pdfBytes("a"), 0o644)
	os.WriteFile(filepath.Join(folder, "b.pdf"), pdfBytes("a"), 0o644) // same contents
	os.WriteFile(filepath.Join(folder, "c.doc"), []byte("word file"), 0o644)
	os.WriteFile(filepath.Join(folder, ".DS_Store"), []byte("x"), 0o644)
	old := time.Date(2019, 6, 1, 12, 0, 0, 0, time.Local)
	os.Chtimes(filepath.Join(folder, "a.pdf"), old, old)

	var rep importReport
	x.json(x.do("GET", "/api/import", "", me), 200, &rep)
	if rep.Waiting != 3 {
		t.Fatalf("waiting %+v", rep)
	}
	x.json(x.do("POST", "/api/import", "", me), 200, nil)
	for range 100 {
		x.json(x.do("GET", "/api/import", "", me), 200, &rep)
		if !rep.Running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rep.Total != 3 || rep.Added != 1 || rep.Dupes != 1 || len(rep.Failed) != 1 || rep.Failed[0].Name != "Receipts/c.doc" {
		t.Fatalf("report %+v", rep)
	}
	var list struct{ Documents []store.Document }
	x.json(x.do("GET", "/api/documents?category=none", "", me), 200, &list)
	if len(list.Documents) != 1 || list.Documents[0].DocDate != "2019-06-01" {
		t.Fatalf("imported %+v", list.Documents)
	}
}

func TestFoyer(t *testing.T) {
	x := newServer(t)
	x.json(x.do("GET", "/api/foyer/widget", "", ""), 401, nil)
	me, dad := x.setup()
	x.upload(dad, "", part{"file", "private.pdf", pdfBytes("dad")})
	x.upload(me, "", part{"file", "mine.pdf", pdfBytes("me")})

	var widget foyerWidget
	x.json(x.do("GET", "/api/foyer/widget", "", token), 200, &widget)
	if widget.Version != 1 || widget.Stats[0].Value != "1" || len(widget.Items) != 1 || widget.Items[0].Title != "mine" {
		t.Fatalf("widget %+v", widget)
	}
	x.json(x.do("GET", widget.Items[0].Image, "", token), 404, nil) // no thumbnail before processing

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	w, _ := mw.CreateFormFile("file", "dropped.pdf")
	w.Write(pdfBytes("drop"))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/foyer/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, req)
	var out map[string]string
	x.json(rec, 200, &out)
	if out["message"] != "Saved to Docvault: saved “dropped”" || !strings.HasPrefix(out["url"], "/documents/") {
		t.Fatalf("drop: %+v", out)
	}
}

func TestCategoriesAndSettings(t *testing.T) {
	x := newServer(t)
	me, dad := x.setup()
	var names []string
	x.json(x.do("GET", "/api/categories?format=names", "", dad), 200, &names)
	if len(names) != len(store.DefaultCategories)+1 || names[0] != "ID" || names[8] != "Banking" {
		t.Fatalf("names %v", names)
	}
	x.json(x.do("PUT", "/api/categories", `[{"name":"ID"}]`, dad), 403, nil)
	x.json(x.do("PUT", "/api/categories", `[{"name":"Uncategorised"}]`, me), 400, nil)
	x.json(x.do("PUT", "/api/categories", `[{"name":"Pets"},{"name":"pets"}]`, me), 409, nil)
	x.json(x.do("PUT", "/api/settings", `{"ocr_langs":"eng","shortcut_url":"http://x"}`, me), 400, nil)
	var set settingsInfo
	x.json(x.do("PUT", "/api/settings", `{"ocr_langs":"eng","shortcut_url":"https://www.icloud.com/shortcuts/abc"}`, me), 200, &set)
	x.json(x.do("GET", "/api/settings", "", dad), 200, &set)
	if set.OCRLangs != "eng" || set.ShortcutURL == "" || set.Classifier != "" {
		t.Fatalf("settings %+v", set)
	}
}

func TestCrossOriginRefused(t *testing.T) {
	x := newServer(t)
	me, _ := x.setup()
	x.json(x.do("POST", "/api/tokens", `{"name":"x"}`, me, "Origin", "https://evil.example"), 403, nil)
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{
		"CamScanner 03-15-2021 10.22.pdf": "2021-03-15",
		"Scan 2024-01-09.pdf":             "2024-01-09",
		"policy 12345678.pdf":             "",
		"CamScanner 13-45-2021.pdf":       "",
	} {
		if got := dateFromName(in); got != want {
			t.Errorf("dateFromName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := safeName("../../etc/pa:ss wd.PDF", ".pdf"); got != "pa_ss wd.pdf" {
		t.Errorf("safeName = %q", got)
	}
}

func TestSPAFallback(t *testing.T) {
	x := newServer(t)
	rec := x.do("GET", "/some/page", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
	if rec := x.do("GET", "/healthz", "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("healthz: %d", rec.Code)
	}
}

type stubClassifier struct{}

func (stubClassifier) Name() string { return "stub" }
func (stubClassifier) Classify(context.Context, process.Input) (*store.Suggest, error) {
	return &store.Suggest{}, nil
}

func TestSuggestions(t *testing.T) {
	x := newServer(t)
	me, _ := x.setup()
	ctx := context.Background()
	x.json(x.do("POST", "/api/suggestions/request", "", me), 409, nil) // off

	// Masking settings and the preview.
	x.json(x.do("PUT", "/api/settings", `{"ocr_langs":"eng","people":[{"name":" Pranav ","aliases":["Pranav Sample",""]},{"name":""}],"mask_words":["Sample"," "]}`, me), 200, nil)
	var masked map[string]string
	x.json(x.do("POST", "/api/mask", `{"text":"Pranav Sample, PAN ABCPS1234K, Sample Nagar"}`, me), 200, &masked)
	if masked["text"] != "[person:1], PAN [pan], [masked] Nagar" {
		t.Fatalf("masked %q", masked["text"])
	}
	x.json(x.do("PUT", "/api/settings", `{"ocr_langs":"eng","classify_from":"everything"}`, me), 400, nil)
	x.json(x.do("PUT", "/api/settings", `{"ocr_langs":"eng","suggest_new":"always"}`, me), 400, nil)
	var set settingsInfo
	x.json(x.do("GET", "/api/settings", "", me), 200, &set)
	if len(set.People) != 1 || len(set.People[0].Aliases) != 1 || len(set.MaskWords) != 1 || set.ClassifyFrom != "auto" || set.SuggestNew != "ask" {
		t.Fatalf("settings %+v", set)
	}

	// The tag list.
	var vocab []store.VocabTag
	x.json(x.do("GET", "/api/tags", "", me), 200, &vocab)
	if len(vocab) < 40 {
		t.Fatalf("vocab %d", len(vocab))
	}
	x.json(x.do("PUT", "/api/tags", `[{"name":"#car"},{"name":"Car"}]`, me), 409, nil)
	x.json(x.do("PUT", "/api/tags", `[{"name":"#car"},{"name":"pets","category_id":1}]`, me), 200, &vocab)
	if len(vocab) != 2 || vocab[1].Name != "car" { // uncategorised tags last
		t.Fatalf("vocab %+v", vocab)
	}

	// Two documents with suggestions; applying all of them in the inbox.
	x.upload(me, "", part{"file", "a.pdf", pdfBytes("a")})
	x.upload(me, "", part{"file", "b.pdf", pdfBytes("b")})
	for range 2 {
		job, _ := x.s.Store.ClaimJob(ctx)
		x.s.Store.FinishJob(ctx, job.ID, store.JobResult{Pages: 1, TextSource: "pdf", Text: "text", Classified: true,
			ClassifierInput: "Title: a\n\ntext", Suggestion: &store.Suggest{Title: "Renamed", Category: "Banking", Tags: []string{"fd"}, NewTags: []string{"locker"}}})
	}
	var facets store.Facets
	x.json(x.do("GET", "/api/facets", "", me), 200, &facets)
	if facets.Suggested != 2 {
		t.Fatalf("facets %+v", facets)
	}
	var in map[string]string
	x.json(x.do("GET", "/api/documents/1/classifier-input", "", me), 200, &in)
	if !strings.HasPrefix(in["text"], "Title: a") {
		t.Fatalf("input %q", in["text"])
	}
	var applied map[string]int
	x.json(x.do("POST", "/api/suggestions/apply?category=none", "", me), 200, &applied)
	if applied["applied"] != 2 {
		t.Fatalf("applied %v", applied)
	}
	var list struct{ Documents []store.Document }
	x.json(x.do("GET", "/api/documents?category=none", "", me), 200, &list)
	if len(list.Documents) != 0 {
		t.Fatal("still in the inbox")
	}
	x.json(x.do("GET", "/api/documents?q=renamed", "", me), 200, &list)
	if len(list.Documents) != 2 || list.Documents[0].Category != "Banking" || strings.Join(list.Documents[0].Tags, ",") != "fd,locker" ||
		list.Documents[0].Suggestion != nil {
		t.Fatalf("after apply %+v", list.Documents)
	}
	// "locker" isn't on the list (the vocabulary was replaced above, so "fd" isn't either).
	var unlisted []store.Count
	x.json(x.do("GET", "/api/tags/unlisted", "", me), 200, &unlisted)
	if len(unlisted) != 2 || unlisted[0].Name != "fd" || unlisted[0].Count != 2 {
		t.Fatalf("unlisted %+v", unlisted)
	}

	// Asking again queues them: all matching, or just the selected ones.
	x.s.Processor.Classifier = stubClassifier{}
	var queued map[string]int
	x.json(x.do("POST", "/api/suggestions/request?q=renamed", "", me), 200, &queued)
	if queued["queued"] != 2 {
		t.Fatalf("queued %v", queued)
	}
	for range 2 {
		job, _ := x.s.Store.ClaimJob(ctx)
		if !job.WantSuggestion {
			t.Fatal("not marked as asked for")
		}
		x.s.Store.FinishJob(ctx, job.ID, store.JobResult{Pages: 1, TextSource: "pdf", Text: "text"})
	}
	x.json(x.do("POST", "/api/suggestions/request", `{"ids":[2, 999]}`, me), 200, &queued)
	if queued["queued"] != 1 {
		t.Fatalf("queued selected %v", queued)
	}
	if job, _ := x.s.Store.ClaimJob(ctx); job == nil || job.ID != 2 {
		t.Fatalf("queued %+v", job)
	}
	x.json(x.do("GET", "/api/settings", "", me), 200, &set)
	if set.Classifier != "stub" {
		t.Fatalf("classifier %q", set.Classifier)
	}
}

func TestFoyerLinkForAdminsOnly(t *testing.T) {
	x := newServer(t)
	x.s.FoyerURL = "https://home.example.com"
	me, dad := x.setup()
	var info sessionInfo
	x.json(x.do("GET", "/api/session", "", me), 200, &info)
	if info.FoyerURL == "" {
		t.Fatal("admin has no Foyer link")
	}
	info = sessionInfo{}
	x.json(x.do("GET", "/api/session", "", dad), 200, &info)
	if info.FoyerURL != "" {
		t.Fatal("Foyer link shown to a member")
	}
	info = sessionInfo{}
	x.json(x.do("GET", "/api/session", "", ""), 200, &info)
	if info.FoyerURL != "" {
		t.Fatal("Foyer link shown before sign-in")
	}
	info = sessionInfo{}
	x.json(x.do("POST", "/api/session", `{"username":"dad","password":"password1"}`, ""), 200, &info)
	if info.FoyerURL != "" {
		t.Fatal("Foyer link in the sign-in answer")
	}
}
