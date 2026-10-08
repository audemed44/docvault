package server

import (
	"errors"
	"net/http"
	"strings"

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
	// Classifier says whether DOCVAULT_CLASSIFIER_URL is set.
	Classifier bool `json:"classifier"`
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
	writeJSON(w, http.StatusOK, settingsInfo{Settings: set, Languages: langs, Classifier: s.Processor.Classifier != nil})
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
	if err := s.Store.SaveSettings(r.Context(), set); err != nil {
		storeError(w, err)
		return
	}
	s.getSettings(w, r)
}
