package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/audemed44/docvault/internal/store"
)

// The bulk import reads a folder on the server, import/<username>/ (for an
// rsync'd CamScanner export), into the user's inbox: file names become
// titles, dates come from the name or the file's time, and duplicates
// (by content) are skipped, so running it again is safe. The files are
// left where they are.

type importReport struct {
	Running  bool           `json:"running"`
	Started  time.Time      `json:"started,omitzero"`
	Finished time.Time      `json:"finished,omitzero"`
	Total    int            `json:"total"`
	Added    int            `json:"added"`
	Dupes    int            `json:"duplicates"`
	Failed   []ingestResult `json:"failed"`
	// Folder is where to put the files, as seen inside the container.
	Folder string `json:"folder"`
	// Waiting counts the files in the folder now.
	Waiting int `json:"waiting"`
}

type imports struct {
	mu      sync.Mutex
	reports map[int64]*importReport
}

func (s *Server) importFolder(u *store.User) string {
	return filepath.Join(s.dir("import"), u.Username)
}

// importFiles lists the files to import, skipping hidden ones.
func importFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && path != root {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func (s *Server) importStatus(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	folder := s.importFolder(u)
	s.imports.mu.Lock()
	rep := importReport{Failed: []ingestResult{}}
	if cur := s.imports.reports[u.ID]; cur != nil {
		rep = *cur
		rep.Failed = append([]ingestResult{}, cur.Failed...)
	}
	s.imports.mu.Unlock()
	rep.Folder = folder
	if !rep.Running {
		rep.Waiting = len(importFiles(folder))
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) startImport(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	folder := s.importFolder(u)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		storeError(w, err)
		return
	}
	files := importFiles(folder)
	s.imports.mu.Lock()
	if cur := s.imports.reports[u.ID]; cur != nil && cur.Running {
		s.imports.mu.Unlock()
		writeError(w, http.StatusConflict, "an import is already running")
		return
	}
	rep := &importReport{Running: true, Started: time.Now(), Total: len(files), Failed: []ingestResult{}}
	s.imports.reports[u.ID] = rep
	s.imports.mu.Unlock()

	go s.runImport(context.WithoutCancel(r.Context()), u, folder, files, rep)
	s.importStatus(w, r)
}

func (s *Server) runImport(ctx context.Context, u *store.User, folder string, files []string, rep *importReport) {
	for _, path := range files {
		res := s.importOne(ctx, u, folder, path)
		s.imports.mu.Lock()
		switch res.Status {
		case "added":
			rep.Added++
		case "duplicate":
			rep.Dupes++
		default:
			res.Document = nil
			rep.Failed = append(rep.Failed, res)
		}
		s.imports.mu.Unlock()
	}
	s.imports.mu.Lock()
	rep.Running, rep.Finished = false, time.Now()
	s.imports.mu.Unlock()
	slog.Info("import done", "user", u.Username, "added", rep.Added, "duplicates", rep.Dupes, "failed", len(rep.Failed))
}

func (s *Server) importOne(ctx context.Context, u *store.User, folder, path string) ingestResult {
	rel, _ := filepath.Rel(folder, path)
	f, err := os.Open(path)
	if err != nil {
		return ingestResult{Name: rel, Status: "failed", Message: err.Error()}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ingestResult{Name: rel, Status: "failed", Message: err.Error()}
	}
	sp, err := s.spool(f, filepath.Base(path))
	if err != nil {
		return ingestResult{Name: rel, Status: "failed", Message: err.Error()}
	}
	res := s.commit(ctx, sp, ingestOptions{User: u, Modified: info.ModTime()})
	res.Name = rel
	return res
}
