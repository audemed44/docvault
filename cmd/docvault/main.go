// Command docvault serves Docvault's API and frontend from one small binary.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the runtime image may have no zoneinfo; TZ needs this

	"github.com/audemed44/docvault/internal/process"
	"github.com/audemed44/docvault/internal/server"
	"github.com/audemed44/docvault/internal/store"
	"github.com/audemed44/docvault/web"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	level := slog.LevelInfo
	if os.Getenv("DOCVAULT_DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	token := os.Getenv("DOCVAULT_TOKEN")
	if token == "" {
		slog.Error("set DOCVAULT_TOKEN: it creates the first account, and Foyer uses it for the widget")
		os.Exit(1)
	}
	dataDir := env("DOCVAULT_DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		slog.Error("could not create the data folder", "err", err)
		os.Exit(1)
	}
	db, err := store.Open(filepath.Join(dataDir, "docvault.db"))
	if err != nil {
		slog.Error("could not open the database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	classifier := newClassifier()
	proc := process.New(process.Options{
		Store: db, Files: filepath.Join(dataDir, "files"), Cache: filepath.Join(dataDir, "cache"), Classifier: classifier,
	})
	app, err := server.New(server.Options{
		Store: db, Processor: proc, Token: token, DataDir: dataDir, FoyerURL: foyerURL(), Web: dist,
	})
	if err != nil {
		slog.Error("could not set up the data folder", "err", err)
		os.Exit(1)
	}
	go proc.Run(ctx)

	srv := &http.Server{
		Addr:              ":" + env("DOCVAULT_PORT", "8080"),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("docvault listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// healthcheck is the image's HEALTHCHECK: the runtime image has no curl.
func healthcheck() int {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + env("DOCVAULT_PORT", "8080") + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return 1
	}
	return 0
}

// newClassifier sets up the optional suggestions: a chat model through
// an OpenAI-compatible API (DOCVAULT_LLM_*), or any URL that takes the
// input as JSON (DOCVAULT_CLASSIFIER_URL).
func newClassifier() process.Classifier {
	if key := os.Getenv("DOCVAULT_LLM_KEY"); key != "" {
		model := os.Getenv("DOCVAULT_LLM_MODEL")
		if model == "" {
			slog.Warn("DOCVAULT_LLM_KEY is set but DOCVAULT_LLM_MODEL isn't; suggestions are off")
			return nil
		}
		llm := process.NewLLM(os.Getenv("DOCVAULT_LLM_URL"), key, model)
		slog.Info("suggestions on: masked text goes to a chat model", "url", llm.URL, "model", model)
		return llm
	}
	if u := os.Getenv("DOCVAULT_CLASSIFIER_URL"); u != "" {
		slog.Info("suggestions on: masked text goes to the classifier", "url", u)
		return process.NewHook(u, os.Getenv("DOCVAULT_CLASSIFIER_TOKEN"))
	}
	return nil
}

// foyerURL is HOMEPAGE_URL, the link back to Foyer in the header, when
// it's an http(s) address.
func foyerURL() string {
	u := os.Getenv("HOMEPAGE_URL")
	if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		slog.Warn("HOMEPAGE_URL isn't an http(s) address; ignoring it", "url", u)
		return ""
	}
	return u
}
