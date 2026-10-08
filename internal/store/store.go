// Package store keeps Docvault's state in SQLite.
//
// The schema is created with CREATE ... IF NOT EXISTS; later changes go in
// migrations, which run once each in order and are recorded in
// PRAGMA user_version.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/audemed44/docvault/internal/mask"
	_ "modernc.org/sqlite" // pure Go, so the build stays static
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE COLLATE NOCASE,
	name     TEXT NOT NULL,
	password TEXT NOT NULL,
	admin    INTEGER NOT NULL DEFAULT 0,
	created  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	hash    TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created INTEGER NOT NULL,
	seen    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS api_tokens (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name    TEXT NOT NULL,
	hash    TEXT NOT NULL UNIQUE,
	hint    TEXT NOT NULL,
	created INTEGER NOT NULL,
	used    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS categories (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	name     TEXT NOT NULL UNIQUE COLLATE NOCASE,
	position INTEGER NOT NULL DEFAULT 0
);
-- owner_id NULL is the shared Family space; otherwise the owner's private
-- library. file_path is the original under files/, kept byte for byte.
CREATE TABLE IF NOT EXISTS documents (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	owner_id    INTEGER REFERENCES users(id),
	added_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
	title       TEXT NOT NULL,
	category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
	doc_date    TEXT NOT NULL,
	expires     TEXT NOT NULL DEFAULT '',
	notes       TEXT NOT NULL DEFAULT '',
	file_name   TEXT NOT NULL,
	file_path   TEXT NOT NULL,
	mime        TEXT NOT NULL,
	size        INTEGER NOT NULL,
	sha256      TEXT NOT NULL,
	pages       INTEGER NOT NULL DEFAULT 0,
	status      TEXT NOT NULL DEFAULT 'pending',
	error       TEXT NOT NULL DEFAULT '',
	text_source TEXT NOT NULL DEFAULT '',
	ocr_lang    TEXT NOT NULL DEFAULT '',
	force_ocr   INTEGER NOT NULL DEFAULT 0,
	suggestion  TEXT NOT NULL DEFAULT '',
	created     INTEGER NOT NULL,
	updated     INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS documents_space_sha ON documents (ifnull(owner_id, 0), sha256);
CREATE INDEX IF NOT EXISTS documents_status ON documents (status);
CREATE INDEX IF NOT EXISTS documents_date ON documents (doc_date);
CREATE TABLE IF NOT EXISTS document_tags (
	document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
	tag         TEXT NOT NULL COLLATE NOCASE,
	PRIMARY KEY (document_id, tag)
);
-- Search index, one row per document (rowid = documents.id). body is the
-- extracted text, which lives only here.
CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5 (
	title, notes, tags, body, tokenize = 'unicode61 remove_diacritics 2'
);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// DefaultCategories are created with a new database; they're editable.
var DefaultCategories = []string{"ID", "Property", "Medical", "Insurance", "Tax", "Vehicle", "Education", "Bills", "Other"}

// migrations run after the schema, once each: append, never edit or
// reorder. migrations[i] moves the database to user_version i+1.
var migrations = []string{
	// 1: the tags the classifier may suggest, each under a category.
	`CREATE TABLE tag_vocab (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
		category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		position    INTEGER NOT NULL DEFAULT 0
	)`,
	// 2: what the classifier was sent (masked), when it last ran, and why
	// it failed.
	`ALTER TABLE documents ADD COLUMN classifier_input TEXT NOT NULL DEFAULT '';
	ALTER TABLE documents ADD COLUMN classified INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE documents ADD COLUMN classify_error TEXT NOT NULL DEFAULT ''`,
	// 3: a Banking category, and the starting tags.
	seedVocab(),
}

// Open opens (or creates) the database.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-1024)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: writes come from one process, and every open SQLite
	// connection holds its own page cache. Closed when idle for a while.
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	// Categories first: the migrations add to them.
	s := &Store{db: db}
	if err := s.seedCategories(); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("database migration: %w", err)
	}
	return s, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("%d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

// Get reads a JSON setting into v; a missing key leaves v as it was.
func (s *Store) Get(ctx context.Context, key string, v any) error {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), v)
}

// Put stores v as a JSON setting.
func (s *Store) Put(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, string(b))
	return err
}

// Settings are the app-wide settings an admin can change.
type Settings struct {
	// OCRLangs are the Tesseract languages for documents without a text
	// layer, joined with "+".
	OCRLangs string `json:"ocr_langs"`
	// ShortcutURL is the iCloud link to the "Save to Vault" Shortcut.
	ShortcutURL string `json:"shortcut_url"`
	// People are masked as "[person:name]" for the classifier, which can
	// tag documents with their names.
	People []mask.Person `json:"people"`
	// MaskWords are masked wherever they appear (a surname, a street).
	MaskWords []string `json:"mask_words"`
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	set := Settings{OCRLangs: "eng+hin"}
	err := s.Get(ctx, "settings", &set)
	if set.People == nil {
		set.People = []mask.Person{}
	}
	if set.MaskWords == nil {
		set.MaskWords = []string{}
	}
	return set, err
}

func (s *Store) SaveSettings(ctx context.Context, set Settings) error {
	return s.Put(ctx, "settings", set)
}

// TagNames are every tag in use, for the classifier to reuse.
func (s *Store) TagNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tag FROM document_tags GROUP BY tag ORDER BY count(*) DESC LIMIT 300`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
