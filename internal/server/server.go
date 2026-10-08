// Package server exposes Docvault's JSON API and the built frontend.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/audemed44/docvault/internal/process"
	"github.com/audemed44/docvault/internal/store"
)

type Options struct {
	Store     *store.Store
	Processor *process.Processor
	Token     string // DOCVAULT_TOKEN: first-run setup and Foyer
	// DataDir holds files/ (the originals), cache/ (thumbnails and photo
	// PDFs), import/ (folders to bulk import) and tmp/ (uploads in flight).
	DataDir  string
	Web      fs.FS
	FoyerURL string // Foyer, the homelab's start page, linked from the header
}

type Server struct {
	Options
	imports imports
}

func New(o Options) (*Server, error) {
	s := &Server{Options: o, imports: imports{reports: map[int64]*importReport{}}}
	for _, d := range []string{"files", "cache", "import", "tmp"} {
		if err := os.MkdirAll(s.dir(d), 0o755); err != nil {
			return nil, err
		}
	}
	// Uploads cut off by a restart.
	if old, _ := filepath.Glob(filepath.Join(s.dir("tmp"), "upload-*")); old != nil {
		for _, f := range old {
			os.Remove(f)
		}
	}
	return s, nil
}

func (s *Server) dir(name string) string { return filepath.Join(s.DataDir, name) }

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("PUT /api/me", sessionOnly(s.updateMe))
	api.HandleFunc("GET /api/tokens", sessionOnly(s.listTokens))
	api.HandleFunc("POST /api/tokens", sessionOnly(s.createToken))
	api.HandleFunc("DELETE /api/tokens/{id}", sessionOnly(s.deleteToken))
	api.HandleFunc("GET /api/users", adminOnly(s.listUsers))
	api.HandleFunc("POST /api/users", adminOnly(s.saveUser))
	api.HandleFunc("PUT /api/users/{id}", adminOnly(s.saveUser))
	api.HandleFunc("DELETE /api/users/{id}", adminOnly(s.deleteUser))

	api.HandleFunc("GET /api/categories", s.listCategories)
	api.HandleFunc("PUT /api/categories", adminOnly(s.saveCategories))
	api.HandleFunc("GET /api/settings", s.getSettings)
	api.HandleFunc("PUT /api/settings", adminOnly(s.saveSettings))

	api.HandleFunc("POST /api/upload", s.upload)
	api.HandleFunc("GET /api/import", s.importStatus)
	api.HandleFunc("POST /api/import", sessionOnly(s.startImport))
	api.HandleFunc("GET /api/facets", s.facets)
	api.HandleFunc("GET /api/documents", s.listDocuments)
	api.HandleFunc("GET /api/documents/{id}", s.getDocument)
	api.HandleFunc("PUT /api/documents/{id}", s.updateDocument)
	api.HandleFunc("DELETE /api/documents/{id}", s.deleteDocument)
	api.HandleFunc("GET /api/documents/{id}/text", s.documentText)
	api.HandleFunc("GET /api/documents/{id}/file", s.documentFile)
	api.HandleFunc("GET /api/documents/{id}/original", s.documentOriginal)
	api.HandleFunc("GET /api/documents/{id}/thumb", s.documentThumb)
	api.HandleFunc("POST /api/documents/{id}/reprocess", s.reprocess)
	api.HandleFunc("POST /api/documents/{id}/suggestion", s.applySuggestion)
	api.HandleFunc("DELETE /api/documents/{id}/suggestion", s.dismissSuggestion)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session", s.getSession)
	mux.HandleFunc("POST /api/session", s.login)
	mux.HandleFunc("DELETE /api/session", s.logout)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("GET /api/foyer/widget", s.requireSystem(s.foyerWidget))
	mux.HandleFunc("GET /api/foyer/thumb/{id}", s.requireSystem(s.foyerThumb))
	mux.HandleFunc("POST /api/foyer/upload", s.requireSystem(s.foyerUpload))
	mux.Handle("/api/", s.requireUser(api))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", s.spa())
	return securityHeaders(sameOrigin(mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// storeError answers 404 for missing things and 500 otherwise.
func storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "already exists")
		return
	}
	slog.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, err.Error())
}

// readJSON decodes a JSON body of at most limit bytes.
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "expected JSON")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}

// spa serves the built frontend, falling back to index.html for app routes.
func (s *Server) spa() http.Handler {
	files := http.FileServer(http.FS(s.Web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if info, err := fs.Stat(s.Web, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(s.Web, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
