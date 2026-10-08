package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Document is one stored file and what's known about it. Documents are
// either in their owner's private library or in the shared Family space.
type Document struct {
	ID         int64    `json:"id"`
	Family     bool     `json:"family"`
	OwnerID    int64    `json:"owner_id,omitempty"`
	AddedBy    string   `json:"added_by"`
	Title      string   `json:"title"`
	CategoryID int64    `json:"category_id"` // 0: uncategorised
	Category   string   `json:"category"`
	DocDate    string   `json:"doc_date"` // YYYY-MM-DD
	Expires    string   `json:"expires"`  // YYYY-MM-DD or empty
	Notes      string   `json:"notes"`
	Tags       []string `json:"tags"`
	FileName   string   `json:"file_name"`
	FilePath   string   `json:"-"` // relative to the files folder
	Mime       string   `json:"mime"`
	Size       int64    `json:"size"`
	SHA256     string   `json:"sha256"`
	Pages      int      `json:"pages"`
	Status     string   `json:"status"` // pending, processing, ready or failed
	Error      string   `json:"error,omitempty"`
	TextSource string   `json:"text_source"` // "", "pdf" (its own text layer) or "ocr"
	OCRLang    string   `json:"ocr_lang,omitempty"`
	Suggestion *Suggest `json:"suggestion,omitempty"`
	// Classified is when the classifier last looked at it (zero: never).
	Classified    time.Time `json:"classified,omitzero"`
	ClassifyError string    `json:"classify_error,omitempty"`
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`
	// Snippet is the matching text, with matches between \x02 and \x03
	// (search results only).
	Snippet string `json:"snippet,omitempty"`
}

// Suggest is what the optional classifier proposes for a document. It's
// only applied when someone accepts it.
type Suggest struct {
	Title    string   `json:"title,omitempty"`
	Category string   `json:"category,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	// NewTags are tags the classifier proposes that aren't on the tag
	// list (yet): shown apart, and added to the document when applied.
	NewTags []string `json:"new_tags,omitempty"`
	DocDate string   `json:"doc_date,omitempty"`
	Expires string   `json:"expires,omitempty"`
}

func (s *Suggest) Empty() bool {
	return s == nil || (s.Title == "" && s.Category == "" && len(s.Tags) == 0 && len(s.NewTags) == 0 &&
		s.DocDate == "" && s.Expires == "")
}

const docCols = `d.id, d.owner_id, ifnull(a.name, ''), d.title, ifnull(d.category_id, 0), ifnull(c.name, ''),
	d.doc_date, d.expires, d.notes, d.file_name, d.file_path, d.mime, d.size, d.sha256, d.pages,
	d.status, d.error, d.text_source, d.ocr_lang, d.suggestion, d.classified, d.classify_error, d.created, d.updated,
	(SELECT ifnull(group_concat(tag, char(31)), '') FROM (SELECT tag FROM document_tags t WHERE t.document_id = d.id ORDER BY tag))`

const docFrom = ` FROM documents d
	LEFT JOIN categories c ON c.id = d.category_id
	LEFT JOIN users a ON a.id = d.added_by`

// visible limits a query to what a user may see: their own library and the
// Family space.
const visible = `(d.owner_id = ? OR d.owner_id IS NULL)`

func scanDoc(row interface{ Scan(...any) error }, extra ...any) (*Document, error) {
	var d Document
	var owner sql.NullInt64
	var suggestion, tags string
	var classified, created, updated int64
	dest := []any{&d.ID, &owner, &d.AddedBy, &d.Title, &d.CategoryID, &d.Category,
		&d.DocDate, &d.Expires, &d.Notes, &d.FileName, &d.FilePath, &d.Mime, &d.Size, &d.SHA256, &d.Pages,
		&d.Status, &d.Error, &d.TextSource, &d.OCRLang, &suggestion, &classified, &d.ClassifyError, &created, &updated, &tags}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	d.Family = !owner.Valid
	d.OwnerID = owner.Int64
	d.Created, d.Updated, d.Classified = fromMS(created), fromMS(updated), fromMS(classified)
	d.Tags = []string{}
	if tags != "" {
		d.Tags = strings.Split(tags, "\x1f")
	}
	if suggestion != "" {
		var sg Suggest
		if json.Unmarshal([]byte(suggestion), &sg) == nil && !sg.Empty() {
			d.Suggestion = &sg
		}
	}
	return &d, nil
}

// Document reads a document the user may see.
func (s *Store) Document(ctx context.Context, userID, id int64) (*Document, error) {
	return scanDoc(s.db.QueryRowContext(ctx, `SELECT `+docCols+docFrom+` WHERE d.id = ? AND `+visible, id, userID))
}

// DocumentText is the extracted text of a document the user may see.
func (s *Store) DocumentText(ctx context.Context, userID, id int64) (string, error) {
	var text string
	err := s.db.QueryRowContext(ctx, `SELECT ifnull(f.body, '') FROM documents d
		LEFT JOIN documents_fts f ON f.rowid = d.id WHERE d.id = ? AND `+visible, id, userID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return text, err
}

// ClassifierInput is the masked text the classifier was last sent for a
// document the user may see.
func (s *Store) ClassifierInput(ctx context.Context, userID, id int64) (string, error) {
	var text string
	err := s.db.QueryRowContext(ctx, `SELECT d.classifier_input FROM documents d WHERE d.id = ? AND `+visible, id, userID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return text, err
}

func ownerArg(d *Document) any {
	if d.Family {
		return nil
	}
	return d.OwnerID
}

func categoryArg(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// Duplicate finds a document with the same contents in the space the
// document is going into.
func (s *Store) Duplicate(ctx context.Context, family bool, ownerID int64, sha string) (*Document, error) {
	space := ownerID
	if family {
		space = 0
	}
	return scanDoc(s.db.QueryRowContext(ctx, `SELECT `+docCols+docFrom+`
		WHERE ifnull(d.owner_id, 0) = ? AND d.sha256 = ?`, space, sha))
}

// CreateDocument adds a document waiting to be processed. FilePath is set
// afterwards with SetFilePath, once the file has its final name.
func (s *Store) CreateDocument(ctx context.Context, d *Document, addedBy int64) error {
	now := time.Now()
	d.Created, d.Updated, d.Status = now, now, "pending"
	d.Tags = cleanTags(d.Tags)
	people, err := s.peopleTags(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO documents (owner_id, added_by, title, category_id, doc_date,
		expires, notes, file_name, file_path, mime, size, sha256, status, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		ownerArg(d), addedBy, d.Title, categoryArg(d.CategoryID), d.DocDate, d.Expires, d.Notes,
		d.FileName, d.FilePath, d.Mime, d.Size, d.SHA256, ms(now), ms(now))
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if d.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	if err := writeTags(ctx, tx, d.ID, d.Tags); err != nil {
		return err
	}
	if err := listTags(ctx, tx, d.Tags, d.CategoryID, people); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO documents_fts (rowid, title, notes, tags, body) VALUES (?, ?, ?, ?, '')`,
		d.ID, d.Title, d.Notes, strings.Join(d.Tags, " ")); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetFilePath(ctx context.Context, id int64, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE documents SET file_path = ? WHERE id = ?`, path, id)
	return err
}

// RemoveDocument deletes a document row whatever its owner (used to undo
// a failed upload).
func (s *Store) RemoveDocument(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM documents_fts WHERE rowid = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func cleanTags(tags []string) []string {
	out := []string{}
	for _, t := range tags {
		t = strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(t), "#")), " ")
		if t == "" || len(t) > 60 || slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, t) }) {
			continue
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out
}

// peopleTags are the family's names, which are tags but never go on the
// tag list. Read before a transaction: the store has one connection.
func (s *Store) peopleTags(ctx context.Context) ([]string, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(set.People))
	for i, p := range set.People {
		out[i] = p.Name
	}
	return out, nil
}

// listTags puts a document's tags that aren't on the tag list yet on it,
// under the document's category (or any category without one), so a tag
// someone adds or accepts is offered next time. Tags already on the list
// stay where they are; people's names are left out.
func listTags(ctx context.Context, tx *sql.Tx, tags []string, categoryID int64, people []string) error {
	for _, t := range tags {
		if slices.ContainsFunc(people, func(p string) bool { return strings.EqualFold(p, t) }) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tag_vocab (name, category_id, position)
			VALUES (?, ?, (SELECT ifnull(max(position), 0) + 1 FROM tag_vocab))`, t, categoryArg(categoryID)); err != nil {
			return err
		}
	}
	return nil
}

func writeTags(ctx context.Context, tx *sql.Tx, id int64, tags []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_tags WHERE document_id = ?`, id); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO document_tags (document_id, tag) VALUES (?, ?)`, id, t); err != nil {
			return err
		}
	}
	return nil
}

// UpdateDocument saves the editable fields of a document the user may see.
// Moving it out of the Family space puts it in the user's own library.
func (s *Store) UpdateDocument(ctx context.Context, userID int64, d *Document) error {
	d.Tags = cleanTags(d.Tags)
	if !d.Family {
		d.OwnerID = userID
	}
	people, err := s.peopleTags(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT owner_id FROM documents d WHERE d.id = ? AND `+visible, d.ID, userID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !d.Family && current.Valid {
		d.OwnerID = current.Int64 // stays with its owner
	}
	_, err = tx.ExecContext(ctx, `UPDATE documents SET owner_id = ?, title = ?, category_id = ?, doc_date = ?,
		expires = ?, notes = ?, updated = ? WHERE id = ?`,
		ownerArg(d), d.Title, categoryArg(d.CategoryID), d.DocDate, d.Expires, d.Notes, ms(time.Now()), d.ID)
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err := writeTags(ctx, tx, d.ID, d.Tags); err != nil {
		return err
	}
	if err := listTags(ctx, tx, d.Tags, d.CategoryID, people); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE documents_fts SET title = ?, notes = ?, tags = ? WHERE rowid = ?`,
		d.Title, d.Notes, strings.Join(d.Tags, " "), d.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearSuggestion drops the classifier's suggestion (applied or dismissed).
func (s *Store) ClearSuggestion(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE documents SET suggestion = '' WHERE id = ? AND (owner_id = ? OR owner_id IS NULL)`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDocument removes a document the user may see and returns it, so
// its files can be removed too.
func (s *Store) DeleteDocument(ctx context.Context, userID, id int64) (*Document, error) {
	d, err := s.Document(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return d, s.RemoveDocument(ctx, id)
}

// ── Search ──────────────────────────────────────────────────────────────

type Filter struct {
	Query    string
	Space    string // "" (everything visible), "mine" or "family"
	Category int64  // 0: any, -1: uncategorised
	Tag      string
	Year     string
	Expiring bool // only documents with an expiry date, soonest first
	Status   string
	// Suggested: with a classifier suggestion waiting. Unclassified:
	// processed, but never seen by the classifier.
	Suggested, Unclassified bool
	Limit                   int
	Offset                  int
}

// ftsQuery turns what someone typed into an FTS5 query: every word must
// match, the last one as a prefix. Quoting each word keeps FTS5 syntax out.
func ftsQuery(q string) string {
	words := strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
	})
	if len(words) == 0 {
		return ""
	}
	for i, w := range words {
		words[i] = `"` + w + `"`
	}
	words[len(words)-1] += "*"
	return strings.Join(words, " ")
}

func searchWhere(userID int64, f Filter) (where []string, args []any, join, order, snippet string) {
	where = []string{visible}
	args = []any{userID}
	order = "d.doc_date DESC, d.id DESC"
	snippet = "''"
	if q := ftsQuery(f.Query); q != "" {
		join = " JOIN documents_fts ON documents_fts.rowid = d.id"
		where = append(where, "documents_fts MATCH ?")
		args = append(args, q)
		order = "bm25(documents_fts, 10.0, 5.0, 5.0, 1.0), d.doc_date DESC"
		snippet = `snippet(documents_fts, 3, char(2), char(3), '…', 14)`
	}
	switch f.Space {
	case "mine":
		where = append(where, "d.owner_id = ?")
		args = append(args, userID)
	case "family":
		where = append(where, "d.owner_id IS NULL")
	}
	switch {
	case f.Category == -1:
		where = append(where, "d.category_id IS NULL")
	case f.Category > 0:
		where = append(where, "d.category_id = ?")
		args = append(args, f.Category)
	}
	if f.Tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM document_tags t WHERE t.document_id = d.id AND t.tag = ?)")
		args = append(args, f.Tag)
	}
	if f.Year != "" {
		where = append(where, "substr(d.doc_date, 1, 4) = ?")
		args = append(args, f.Year)
	}
	if f.Expiring {
		where = append(where, "d.expires != ''")
		order = "d.expires, d.id"
	}
	if f.Status != "" {
		where = append(where, "d.status = ?")
		args = append(args, f.Status)
	}
	if f.Suggested {
		where = append(where, "d.suggestion != ''")
	}
	if f.Unclassified {
		where = append(where, "d.classified = 0 AND d.status = 'ready'")
	}
	return where, args, join, order, snippet
}

// Search lists the documents a user may see that match the filter, with
// the total count for paging.
func (s *Store) Search(ctx context.Context, userID int64, f Filter) ([]Document, int, error) {
	where, args, join, order, snippet := searchWhere(userID, f)
	cond := " WHERE " + strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM documents d`+join+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 60
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+docCols+`, `+snippet+docFrom+join+cond+
		` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, f.Limit, max(f.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		var snip string
		d, err := scanDoc(rows, &snip)
		if err != nil {
			return nil, 0, err
		}
		d.Snippet = strings.TrimSpace(snip)
		out = append(out, *d)
	}
	return out, total, rows.Err()
}

// MatchingIDs lists every document the user may see that matches the
// filter (for actions on all of them).
func (s *Store) MatchingIDs(ctx context.Context, userID int64, f Filter) ([]int64, error) {
	where, args, join, _, _ := searchWhere(userID, f)
	rows, err := s.db.QueryContext(ctx, `SELECT d.id FROM documents d`+join+` WHERE `+strings.Join(where, " AND ")+` ORDER BY d.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Facets are the counts behind the library's filters.
type Facets struct {
	Total      int `json:"total"`
	Mine       int `json:"mine"`
	Family     int `json:"family"`
	Inbox      int `json:"inbox"` // uncategorised
	Expiring   int `json:"expiring"`
	Processing int `json:"processing"`
	Failed     int `json:"failed"`
	Suggested  int `json:"suggested"`
	// Unclassified are processed documents the classifier hasn't seen.
	Unclassified int        `json:"unclassified"`
	Categories   []Category `json:"categories"`
	Tags         []Count    `json:"tags"`
	Years        []Count    `json:"years"`
}

type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ExpiringDays is how far ahead "expiring" looks.
const ExpiringDays = 60

func (s *Store) Facets(ctx context.Context, userID int64) (*Facets, error) {
	f := &Facets{}
	soon := time.Now().AddDate(0, 0, ExpiringDays).Format(time.DateOnly)
	err := s.db.QueryRowContext(ctx, `SELECT count(*),
		count(*) FILTER (WHERE d.owner_id = ?),
		count(*) FILTER (WHERE d.owner_id IS NULL),
		count(*) FILTER (WHERE d.category_id IS NULL),
		count(*) FILTER (WHERE d.expires != '' AND d.expires <= ?),
		count(*) FILTER (WHERE d.status IN ('pending', 'processing')),
		count(*) FILTER (WHERE d.status = 'failed'),
		count(*) FILTER (WHERE d.suggestion != ''),
		count(*) FILTER (WHERE d.classified = 0 AND d.status = 'ready')
		FROM documents d WHERE `+visible, userID, soon, userID).
		Scan(&f.Total, &f.Mine, &f.Family, &f.Inbox, &f.Expiring, &f.Processing, &f.Failed, &f.Suggested, &f.Unclassified)
	if err != nil {
		return nil, err
	}
	if f.Categories, err = s.Categories(ctx, userID); err != nil {
		return nil, err
	}
	if f.Tags, err = s.counts(ctx, `SELECT t.tag, count(*) FROM document_tags t JOIN documents d ON d.id = t.document_id
		WHERE `+visible+` GROUP BY t.tag ORDER BY count(*) DESC, t.tag LIMIT 200`, userID); err != nil {
		return nil, err
	}
	if f.Years, err = s.counts(ctx, `SELECT substr(d.doc_date, 1, 4) y, count(*) FROM documents d
		WHERE `+visible+` GROUP BY y ORDER BY y DESC`, userID); err != nil {
		return nil, err
	}
	return f, nil
}

func (s *Store) counts(ctx context.Context, query string, args ...any) ([]Count, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Count{}
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── Categories ──────────────────────────────────────────────────────────

type Category struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"` // documents the user can see
}

func (s *Store) seedCategories() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM categories`).Scan(&n); err != nil || n > 0 {
		return err
	}
	var done bool
	if err := s.Get(context.Background(), "categories_seeded", &done); err != nil || done {
		return err
	}
	for i, name := range DefaultCategories {
		if _, err := s.db.Exec(`INSERT INTO categories (name, position) VALUES (?, ?)`, name, i); err != nil {
			return err
		}
	}
	return s.Put(context.Background(), "categories_seeded", true)
}

// Categories lists the categories in order, with how many documents the
// user can see in each.
func (s *Store) Categories(ctx context.Context, userID int64) ([]Category, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.name,
		(SELECT count(*) FROM documents d WHERE d.category_id = c.id AND `+visible+`)
		FROM categories c ORDER BY c.position, c.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CategoryByName finds a category, ignoring case.
func (s *Store) CategoryByName(ctx context.Context, name string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name = ?`, strings.TrimSpace(name)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// SaveCategories replaces the category list with cats, in that order:
// entries with an ID are renamed, without one are added, and categories
// left out are deleted (their documents become uncategorised).
func (s *Store) SaveCategories(ctx context.Context, cats []Category) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := []string{}
	// Park the names first, so swapping two names doesn't trip UNIQUE.
	if _, err := tx.ExecContext(ctx, `UPDATE categories SET name = '\x00' || id`); err != nil {
		return err
	}
	for i, c := range cats {
		if c.ID > 0 {
			res, err := tx.ExecContext(ctx, `UPDATE categories SET name = ?, position = ? WHERE id = ?`, c.Name, i, c.ID)
			if isUnique(err) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNotFound
			}
			keep = append(keep, strconv.FormatInt(c.ID, 10))
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO categories (name, position) VALUES (?, ?)`, c.Name, i)
		if isUnique(err) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		keep = append(keep, strconv.FormatInt(id, 10))
	}
	del := `DELETE FROM categories`
	if len(keep) > 0 {
		del += ` WHERE id NOT IN (` + strings.Join(keep, ",") + `)`
	}
	if _, err := tx.ExecContext(ctx, del); err != nil {
		return err
	}
	return tx.Commit()
}

// ── Processing jobs ─────────────────────────────────────────────────────

// Job is a document waiting for its thumbnail, text and suggestion.
type Job struct {
	ID       int64
	FilePath string
	Mime     string
	ForceOCR bool
	OCRLang  string
	Title    string
	// TextSource and Text are what an earlier run found, so OCR isn't
	// repeated unless it's asked for.
	TextSource string
	Text       string
	// WantSuggestion: someone asked for a suggestion for it.
	WantSuggestion bool
	// OwnerID is whose library it's in (0: the Family space).
	OwnerID int64
}

// JobResult is what processing found out.
type JobResult struct {
	Pages      int
	Text       string
	TextSource string
	OCRLang    string
	Error      string
	// Classified: the classifier answered, with Suggestion (maybe nil) for
	// ClassifierInput. ClassifyError: it was asked, and failed.
	Classified      bool
	Suggestion      *Suggest
	ClassifierInput string
	ClassifyError   string
}

// ResetJobs puts documents that were being processed when the app stopped
// back in the queue.
func (s *Store) ResetJobs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE documents SET status = 'pending' WHERE status = 'processing'`)
	return err
}

// ClaimJob takes the oldest waiting document, or returns ErrNotFound.
func (s *Store) ClaimJob(ctx context.Context) (*Job, error) {
	var j Job
	err := s.db.QueryRowContext(ctx, `UPDATE documents SET status = 'processing', error = ''
		WHERE id = (SELECT id FROM documents WHERE status = 'pending' ORDER BY id LIMIT 1)
		RETURNING id, file_path, mime, force_ocr, ocr_lang, title, text_source, want_suggestion, ifnull(owner_id, 0)`).
		Scan(&j.ID, &j.FilePath, &j.Mime, &j.ForceOCR, &j.OCRLang, &j.Title, &j.TextSource, &j.WantSuggestion, &j.OwnerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT ifnull(body, '') FROM documents_fts WHERE rowid = ?`, j.ID).Scan(&j.Text)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return &j, err
}

// FinishJob saves what processing found, unless the document was queued
// again (or deleted) meanwhile.
func (s *Store) FinishJob(ctx context.Context, id int64, r JobResult) error {
	status := "ready"
	if r.Error != "" {
		status = "failed"
	}
	suggestion := ""
	if !r.Suggestion.Empty() {
		b, _ := json.Marshal(r.Suggestion)
		suggestion = string(b)
	}
	var classified int64
	if r.Classified {
		classified = ms(time.Now())
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE documents SET status = ?, error = ?, pages = ?, text_source = ?,
		ocr_lang = ?, force_ocr = 0, want_suggestion = 0, classify_error = ?,
		suggestion = CASE WHEN ? THEN ? ELSE suggestion END,
		classified = CASE WHEN ? THEN ? ELSE classified END,
		classifier_input = CASE WHEN ? != '' THEN ? ELSE classifier_input END
		WHERE id = ? AND status = 'processing'`,
		status, r.Error, r.Pages, r.TextSource, r.OCRLang, r.ClassifyError,
		r.Classified, suggestion, r.Classified, classified, r.ClassifierInput, r.ClassifierInput, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE documents_fts SET body = ? WHERE rowid = ?`, r.Text, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Requeue processes a document the user may see again, with OCR forced
// when forceOCR is set (in lang, or the default when it's empty), and a
// suggestion asked for when suggest is set.
func (s *Store) Requeue(ctx context.Context, userID, id int64, forceOCR bool, lang string, suggest bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE documents SET status = 'pending', error = '', force_ocr = ?,
		ocr_lang = CASE WHEN ? THEN ? ELSE ocr_lang END,
		want_suggestion = CASE WHEN ? THEN 1 ELSE want_suggestion END
		WHERE id = ? AND (owner_id = ? OR owner_id IS NULL)`, forceOCR, forceOCR, lang, suggest, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Foyer ───────────────────────────────────────────────────────────────

// AdminDocuments lists what the admins can see (their libraries and the
// Family space) for Foyer: documents expiring soonest, then the newest.
func (s *Store) AdminDocuments(ctx context.Context, f Filter) ([]Document, int, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE admin ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return []Document{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return s.Search(ctx, id, f)
}

// FirstAdmin is the account Foyer's Drop uploads go to.
func (s *Store) FirstAdmin(ctx context.Context) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE admin ORDER BY id LIMIT 1`))
}
