package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func addUser(t *testing.T, s *Store, name string) *User {
	t.Helper()
	u := &User{Username: name, Name: name}
	if err := s.CreateUser(context.Background(), u, "hash"); err != nil {
		t.Fatal(err)
	}
	return u
}

func addDoc(t *testing.T, s *Store, owner *User, family bool, title, sha, text string) *Document {
	t.Helper()
	ctx := context.Background()
	d := &Document{Family: family, OwnerID: owner.ID, Title: title, DocDate: "2024-05-01",
		FileName: title + ".pdf", Mime: "application/pdf", SHA256: sha}
	if err := s.CreateDocument(ctx, d, owner.ID); err != nil {
		t.Fatal(err)
	}
	j, err := s.ClaimJob(ctx)
	if err != nil || j.ID != d.ID {
		t.Fatalf("claim: %v %+v", err, j)
	}
	if err := s.FinishJob(ctx, d.ID, JobResult{Pages: 1, Text: text, TextSource: "pdf"}); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSpacesAndSearch(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	me, dad := addUser(t, s, "me"), addUser(t, s, "dad")

	addDoc(t, s, me, false, "Passport", "a", "Republic of India passport number Z1234567")
	addDoc(t, s, dad, false, "Pension slip", "b", "monthly pension statement")
	addDoc(t, s, me, true, "House deed", "c", "आधार कार्ड भारत सरकार property registration")

	docs, total, err := s.Search(ctx, me.ID, Filter{})
	if err != nil || total != 2 || len(docs) != 2 {
		t.Fatalf("mine + family: %d %v", total, err)
	}
	if docs, _, _ := s.Search(ctx, dad.ID, Filter{Query: "passport"}); len(docs) != 0 {
		t.Fatal("dad sees my passport")
	}
	docs, _, _ = s.Search(ctx, me.ID, Filter{Query: "passp"})
	if len(docs) != 1 || docs[0].Title != "Passport" || docs[0].Snippet == "" {
		t.Fatalf("prefix search: %+v", docs)
	}
	docs, _, _ = s.Search(ctx, dad.ID, Filter{Query: "आधार कार्ड"})
	if len(docs) != 1 || !docs[0].Family {
		t.Fatalf("Hindi search: %+v", docs)
	}
	if docs, _, _ := s.Search(ctx, dad.ID, Filter{Query: `"; DROP TABLE x --`}); len(docs) != 0 {
		t.Fatal("odd query matched")
	}

	if _, err := s.Duplicate(ctx, true, 0, "c"); err != nil {
		t.Fatalf("duplicate in family: %v", err)
	}
	if _, err := s.Duplicate(ctx, false, dad.ID, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("my file counts as dad's duplicate")
	}

	f, err := s.Facets(ctx, me.ID)
	if err != nil || f.Total != 2 || f.Mine != 1 || f.Family != 1 || f.Inbox != 2 || f.Unclassified != 2 || len(f.Categories) != len(DefaultCategories)+3 {
		t.Fatalf("facets %+v %v", f, err)
	}
	if err := s.DeleteUser(ctx, dad.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete user with documents: %v", err)
	}
}

func TestUpdateAndTags(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	me, dad := addUser(t, s, "me"), addUser(t, s, "dad")
	d := addDoc(t, s, me, false, "Policy", "a", "")
	cat, err := s.CategoryByName(ctx, "insurance")
	if err != nil {
		t.Fatal(err)
	}
	d.CategoryID, d.Tags, d.Family, d.Notes = cat, []string{"#car", "Car", " renewal "}, true, "renew in march"
	if err := s.UpdateDocument(ctx, me.ID, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.Document(ctx, dad.ID, d.ID)
	if err != nil {
		t.Fatalf("dad can't see the shared doc: %v", err)
	}
	if got.Category != "Insurance" || len(got.Tags) != 2 || got.Tags[0] != "car" || !got.Family {
		t.Fatalf("got %+v", got)
	}
	if docs, _, _ := s.Search(ctx, dad.ID, Filter{Query: "march", Tag: "CAR"}); len(docs) != 1 {
		t.Fatal("notes/tag search")
	}
	// Dad takes it into his own library.
	got.Family = false
	if err := s.UpdateDocument(ctx, dad.ID, got); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Document(ctx, me.ID, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("still visible to me")
	}
}

func TestJobsAndCategories(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	me := addUser(t, s, "me")
	d := addDoc(t, s, me, false, "Scan", "a", "")
	if err := s.Requeue(ctx, me.ID, d.ID, true, "hin", false); err != nil {
		t.Fatal(err)
	}
	j, err := s.ClaimJob(ctx)
	if err != nil || !j.ForceOCR || j.OCRLang != "hin" {
		t.Fatalf("requeued job %+v %v", j, err)
	}
	// Queued again while processing: the old result is dropped.
	s.Requeue(ctx, me.ID, d.ID, false, "", false)
	s.FinishJob(ctx, d.ID, JobResult{Error: "boom"})
	if got, _ := s.Document(ctx, me.ID, d.ID); got.Status != "pending" {
		t.Fatalf("status %q", got.Status)
	}
	s.ClaimJob(ctx)
	s.FinishJob(ctx, d.ID, JobResult{Error: "no text", Classified: true, Suggestion: &Suggest{Category: "Tax"}})
	got, _ := s.Document(ctx, me.ID, d.ID)
	if got.Status != "failed" || got.Error != "no text" || got.Suggestion == nil || got.Suggestion.Category != "Tax" {
		t.Fatalf("finished %+v", got)
	}

	cats, _ := s.Categories(ctx, me.ID)
	cats[0].Name, cats[1].Name = cats[1].Name, cats[0].Name // swap
	cats = append(cats[:2], Category{Name: "Pets"})
	if err := s.SaveCategories(ctx, cats); err != nil {
		t.Fatal(err)
	}
	cats, _ = s.Categories(ctx, me.ID)
	if len(cats) != 3 || cats[0].Name != "Property" || cats[2].Name != "Pets" {
		t.Fatalf("categories %+v", cats)
	}
	if err := s.SaveCategories(ctx, []Category{{Name: "a"}, {Name: "A"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate names: %v", err)
	}
}

func TestVocab(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	cats, _ := s.Categories(ctx, 0)
	names := []string{}
	for _, c := range cats {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "ID,Property,Medical,Insurance,Tax,Vehicle,Education,Bills,Banking & Investments,Travel,Work,Other" {
		t.Fatalf("categories %s", got)
	}
	vocab, err := s.TagVocab(ctx)
	if err != nil || len(vocab) < 40 || vocab[0].Name != "aadhaar" || vocab[0].CategoryID != cats[0].ID {
		t.Fatalf("vocab %v %+v", err, vocab[:2])
	}
	if err := s.SaveTagVocab(ctx, []VocabTag{{Name: "x"}, {Name: "X"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := s.SaveTagVocab(ctx, []VocabTag{{Name: "pets", CategoryID: cats[1].ID}, {Name: "misc"}}); err != nil {
		t.Fatal(err)
	}
	vocab, _ = s.TagVocab(ctx)
	if len(vocab) != 2 || vocab[0].Name != "pets" || vocab[1].CategoryID != 0 {
		t.Fatalf("saved %+v", vocab)
	}
	ids, err := s.MatchingIDs(ctx, 1, Filter{Unclassified: true})
	if err != nil || len(ids) != 0 {
		t.Fatal(ids, err)
	}
}

func TestUsedTagsScope(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	me, dad := addUser(t, s, "me"), addUser(t, s, "dad")
	mine := addDoc(t, s, me, false, "Mine", "a", "")
	mine.Tags = []string{"travel"}
	s.UpdateDocument(ctx, me.ID, mine)
	shared := addDoc(t, s, me, true, "Shared", "b", "")
	shared.Tags, shared.Family = []string{"house"}, true
	s.UpdateDocument(ctx, me.ID, shared)
	private := addDoc(t, s, dad, false, "Dad's", "c", "")
	private.Tags = []string{"secret-thing"}
	s.UpdateDocument(ctx, dad.ID, private)

	used, _ := s.UsedTags(ctx, me.ID)
	if strings.Join(used, ",") != "house,travel" {
		t.Fatalf("mine: %v", used)
	}
	if used, _ := s.UsedTags(ctx, 0); strings.Join(used, ",") != "house" {
		t.Fatalf("family: %v", used)
	}
	unlisted, _ := s.UnlistedTags(ctx, dad.ID)
	if len(unlisted) != 2 {
		t.Fatalf("dad's unlisted: %+v", unlisted)
	}
}
