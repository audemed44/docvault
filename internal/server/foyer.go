package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/audemed44/docvault/internal/store"
)

// Docvault serves a card in the Foyer widget format
// (https://github.com/audemed44/foyer/blob/main/docs/app-widgets.md).
// Foyer finds it at /api/foyer/widget by itself and calls it with
// DOCVAULT_TOKEN as a bearer token. The card shows what the first admin
// can see (their library and the Family space): documents expiring soon,
// or the newest. "accepts" lets Foyer's Drop save files to their inbox.

type foyerStat struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	Unit    string `json:"unit,omitempty"`
	Caption string `json:"caption,omitempty"`
	Tone    string `json:"tone,omitempty"` // good, warn, bad or accent
}

type foyerItem struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Image    string `json:"image,omitempty"`
	URL      string `json:"url,omitempty"`
}

type foyerAccepts struct {
	URL   string   `json:"url"`
	Types []string `json:"types"`
	Label string   `json:"label,omitempty"`
}

type foyerWidget struct {
	Version     int           `json:"version"`
	Stats       []foyerStat   `json:"stats"`
	ItemsTitle  string        `json:"items_title,omitempty"`
	ItemsLayout string        `json:"items_layout,omitempty"` // list or covers
	Items       []foyerItem   `json:"items"`
	Accepts     *foyerAccepts `json:"accepts,omitempty"`
}

// expiryCaption says when a document expires, relative to today.
func expiryCaption(expires string, now time.Time) string {
	t, err := time.ParseInLocation(time.DateOnly, expires, time.Local)
	if err != nil {
		return ""
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	days := int(t.Sub(today).Hours() / 24)
	switch {
	case days < 0:
		return "expired"
	case days == 0:
		return "expires today"
	case days == 1:
		return "expires tomorrow"
	}
	return fmt.Sprintf("expires in %d days", days)
}

func (s *Server) foyerWidget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := foyerWidget{Version: 1, Stats: []foyerStat{}, Items: []foyerItem{},
		Accepts: &foyerAccepts{URL: "/api/foyer/upload", Types: []string{".pdf", "image/*"}, Label: "Save to Docvault"}}
	admin, err := s.Store.FirstAdmin(ctx)
	if errors.Is(err, store.ErrNotFound) {
		out.Stats = append(out.Stats, foyerStat{Label: "Set up", Value: "—", Caption: "no accounts yet"})
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err != nil {
		storeError(w, err)
		return
	}
	f, err := s.Store.Facets(ctx, admin.ID)
	if err != nil {
		storeError(w, err)
		return
	}
	out.Stats = append(out.Stats,
		foyerStat{Label: "Documents", Value: strconv.Itoa(f.Total), Tone: "accent"},
		foyerStat{Label: "Inbox", Value: strconv.Itoa(f.Inbox), Caption: "to sort"},
	)
	expiring := foyerStat{Label: "Expiring", Value: strconv.Itoa(f.Expiring), Caption: fmt.Sprintf("in %d days", store.ExpiringDays)}
	if f.Expiring > 0 {
		expiring.Tone = "warn"
	}
	out.Stats = append(out.Stats, expiring)
	if f.Failed > 0 {
		out.Stats = append(out.Stats, foyerStat{Label: "Failed", Value: strconv.Itoa(f.Failed), Caption: "processing", Tone: "bad"})
	} else if f.Processing > 0 {
		out.Stats = append(out.Stats, foyerStat{Label: "Processing", Value: strconv.Itoa(f.Processing)})
	}

	now := time.Now()
	soon := now.AddDate(0, 0, store.ExpiringDays).Format(time.DateOnly)
	docs, _, err := s.Store.Search(ctx, admin.ID, store.Filter{Expiring: true, Limit: 12})
	if err != nil {
		storeError(w, err)
		return
	}
	for _, d := range docs {
		if d.Expires > soon {
			break
		}
		out.Items = append(out.Items, foyerDoc(d, expiryCaption(d.Expires, now)))
	}
	if len(out.Items) > 0 {
		out.ItemsTitle = "Expiring soon"
	} else {
		docs, _, err = s.Store.Search(ctx, admin.ID, store.Filter{Limit: 8})
		if err != nil {
			storeError(w, err)
			return
		}
		for _, d := range docs {
			out.Items = append(out.Items, foyerDoc(d, d.Category))
		}
		out.ItemsTitle = "Recent"
	}
	out.ItemsLayout = "covers"
	writeJSON(w, http.StatusOK, out)
}

func foyerDoc(d store.Document, caption string) foyerItem {
	sub := d.Category
	if sub == "" {
		sub = "Inbox"
	}
	if d.Family {
		sub += " · Family"
	}
	return foyerItem{
		Title: d.Title, Subtitle: sub, Caption: caption,
		Image: fmt.Sprintf("/api/foyer/thumb/%d", d.ID), URL: fmt.Sprintf("/documents/%d", d.ID),
	}
}

// foyerThumb serves thumbnails for the card's covers.
func (s *Server) foyerThumb(w http.ResponseWriter, r *http.Request) {
	admin, err := s.Store.FirstAdmin(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	s.documentThumb(w, r.WithContext(withUser(r.Context(), admin)))
}

// foyerUpload saves files dropped in Foyer to the first admin's inbox.
func (s *Server) foyerUpload(w http.ResponseWriter, r *http.Request) {
	admin, err := s.Store.FirstAdmin(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, "set up Docvault first")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	_, files, err := s.readUpload(r)
	defer func() {
		for _, f := range files {
			f.discard()
		}
	}()
	if err != nil || len(files) == 0 {
		msg := "no file"
		if err != nil {
			msg = err.Error()
		}
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	results := []ingestResult{}
	for _, f := range files {
		results = append(results, s.commit(r.Context(), f, ingestOptions{User: admin}))
	}
	files = nil
	status, msg := summarize(results)
	if status != http.StatusOK {
		writeError(w, status, msg[len("error: "):])
		return
	}
	out := map[string]string{"message": "Saved to Docvault: " + msg[len("ok: "):]}
	if len(results) == 1 && results[0].Document != nil {
		out["url"] = fmt.Sprintf("/documents/%d", results[0].Document.ID)
	}
	writeJSON(w, http.StatusOK, out)
}
