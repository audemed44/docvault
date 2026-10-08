package server

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/audemed44/docvault/internal/mask"
	"github.com/audemed44/docvault/internal/process"
	"github.com/audemed44/docvault/internal/store"
)

// listCategories answers the category list. ?format=names gives just the
// names, which the iOS Shortcut turns into its menu.
func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.Store.Categories(r.Context(), currentUser(r).ID)
	if err != nil {
		storeError(w, err)
		return
	}
	if r.URL.Query().Get("format") == "names" {
		names := []string{}
		for _, c := range cats {
			names = append(names, c.Name)
		}
		writeJSON(w, http.StatusOK, names)
		return
	}
	writeJSON(w, http.StatusOK, cats)
}

// saveCategories replaces the list (see store.SaveCategories).
func (s *Server) saveCategories(w http.ResponseWriter, r *http.Request) {
	var cats []store.Category
	if !readJSON(w, r, 64<<10, &cats) {
		return
	}
	for i := range cats {
		cats[i].Name = strings.TrimSpace(cats[i].Name)
		if n := cats[i].Name; n == "" || len(n) > 40 || strings.EqualFold(n, "uncategorised") || strings.EqualFold(n, "inbox") {
			writeError(w, http.StatusBadRequest, "category names need 1–40 characters (and “Uncategorised” is built in)")
			return
		}
	}
	if err := s.Store.SaveCategories(r.Context(), cats); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "two categories have the same name")
			return
		}
		storeError(w, err)
		return
	}
	s.listCategories(w, r)
}

type settingsInfo struct {
	store.Settings
	// Languages are the installed OCR languages.
	Languages []string `json:"languages"`
	// Classifier is what suggests ("llm:<model>" or "hook"), or "" when
	// suggestions are off.
	Classifier string `json:"classifier"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.Store.Settings(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	langs := process.Languages()
	if langs == nil {
		langs = []string{}
	}
	set.OCRWorkers, set.SuggestWorkers = s.Processor.Limits(set)
	info := settingsInfo{Settings: set, Languages: langs}
	if c := s.Processor.Classifier; c != nil {
		info.Classifier = c.Name()
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var set store.Settings
	if !readJSON(w, r, 16<<10, &set) {
		return
	}
	set.OCRLangs = strings.TrimSpace(set.OCRLangs)
	set.ShortcutURL = strings.TrimSpace(set.ShortcutURL)
	if !validLangs(set.OCRLangs) {
		writeError(w, http.StatusBadRequest, "unknown OCR language "+set.OCRLangs)
		return
	}
	if set.ShortcutURL != "" && !strings.HasPrefix(set.ShortcutURL, "https://") {
		writeError(w, http.StatusBadRequest, "the Shortcut link must be an https:// address")
		return
	}
	people := []mask.Person{}
	for _, p := range set.People {
		p.Name = strings.Join(strings.Fields(p.Name), " ")
		if p.Name == "" {
			continue
		}
		aliases := []string{}
		for _, a := range p.Aliases {
			if a = strings.Join(strings.Fields(a), " "); a != "" && len(a) <= 80 {
				aliases = append(aliases, a)
			}
		}
		if len(p.Name) > 40 || len(aliases) > 20 {
			writeError(w, http.StatusBadRequest, "a person's name is too long, or has too many other names")
			return
		}
		people = append(people, mask.Person{Name: p.Name, Aliases: aliases})
	}
	words := []string{}
	for _, w := range set.MaskWords {
		if w = strings.Join(strings.Fields(w), " "); w != "" && len(w) <= 80 {
			words = append(words, w)
		}
	}
	if len(people) > 30 || len(words) > 200 {
		writeError(w, http.StatusBadRequest, "too many people or words to mask")
		return
	}
	set.People, set.MaskWords = people, words
	switch set.SuggestNew {
	case "ask", "auto":
	case "":
		set.SuggestNew = "ask"
	default:
		writeError(w, http.StatusBadRequest, "suggest_new must be ask or auto")
		return
	}
	switch set.ClassifyFrom {
	case "auto", "title", "text":
	case "":
		set.ClassifyFrom = "auto"
	default:
		writeError(w, http.StatusBadRequest, "classify_from must be auto, title or text")
		return
	}
	// 0 keeps the default.
	if set.OCRWorkers < 0 || set.OCRWorkers > process.MaxOCRWorkers {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("documents read at once: 1–%d", process.MaxOCRWorkers))
		return
	}
	if set.SuggestWorkers < 0 || set.SuggestWorkers > process.MaxSuggestWorkers {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("suggestions at once: 1–%d", process.MaxSuggestWorkers))
		return
	}
	if err := s.Store.SaveSettings(r.Context(), set); err != nil {
		storeError(w, err)
		return
	}
	s.Processor.Wake() // the worker limits may have changed
	s.getSettings(w, r)
}

// maskPreview shows what text looks like once masked with the current
// settings, before anything is sent anywhere.
func (s *Server) maskPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, 256<<10, &body) {
		return
	}
	set, err := s.Store.Settings(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"text": mask.Text(body.Text, mask.Options{People: set.People, Words: set.MaskWords}),
	})
}

func (s *Server) listTagVocab(w http.ResponseWriter, r *http.Request) {
	tags, err := s.Store.TagVocab(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

// unlistedTags are tags on documents that aren't on the tag list.
func (s *Server) unlistedTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.Store.UnlistedTags(r.Context(), currentUser(r).ID)
	if err != nil {
		storeError(w, err)
		return
	}
	set, err := s.Store.Settings(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	out := []store.Count{}
	for _, t := range tags {
		if !slices.ContainsFunc(set.People, func(p mask.Person) bool { return strings.EqualFold(p.Name, t.Name) }) {
			out = append(out, t)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// saveTagVocab replaces the tags the classifier may suggest.
func (s *Server) saveTagVocab(w http.ResponseWriter, r *http.Request) {
	var tags []store.VocabTag
	if !readJSON(w, r, 64<<10, &tags) {
		return
	}
	for i := range tags {
		tags[i].Name = strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(tags[i].Name), "#")), " ")
		if n := tags[i].Name; n == "" || len(n) > 40 {
			writeError(w, http.StatusBadRequest, "tags need 1–40 characters")
			return
		}
	}
	if err := s.Store.SaveTagVocab(r.Context(), tags); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "a tag is listed twice: "+strings.TrimPrefix(err.Error(), store.ErrConflict.Error()+": "))
			return
		}
		storeError(w, err)
		return
	}
	s.listTagVocab(w, r)
}
