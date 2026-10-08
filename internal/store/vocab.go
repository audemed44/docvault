package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// The tag vocabulary is the list of tags the classifier may suggest, each
// filed under a category to help it choose. People can still type any tag
// on a document by hand.

// defaultVocab is the starting list, by category (the migration that adds
// it runs once; after that, admins edit it).
var defaultVocab = []struct {
	category string
	tags     []string
}{
	{"ID", []string{"aadhaar", "pan", "passport", "voter-id", "birth-certificate"}},
	{"Property", []string{"sale-deed", "rent-agreement", "property-tax", "society"}},
	{"Medical", []string{"prescription", "lab-report", "discharge-summary", "imaging", "vaccination"}},
	{"Insurance", []string{"health", "life", "motor", "home", "premium-receipt", "claim"}},
	{"Tax", []string{"itr", "form-16", "form-26as", "investment-proof"}},
	{"Vehicle", []string{"rc", "puc", "service"}},
	{"Education", []string{"marksheet", "degree", "certificate"}},
	{"Bills", []string{"electricity", "water", "gas", "phone", "internet", "invoice", "warranty"}},
	{"Banking", []string{"fd", "loan", "cheque", "account-opening"}},
}

func sqlString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// seedVocab is the migration adding Banking (before Other) and the tags.
func seedVocab() string {
	var b strings.Builder
	b.WriteString(`INSERT OR IGNORE INTO categories (name, position)
		VALUES ('Banking', ifnull((SELECT position FROM categories WHERE name = 'Other'), (SELECT ifnull(max(position), 0) + 1 FROM categories)));
	UPDATE categories SET position = position + 1 WHERE name = 'Other';
	`)
	n := 0
	for _, group := range defaultVocab {
		for _, t := range group.tags {
			fmt.Fprintf(&b, "INSERT OR IGNORE INTO tag_vocab (name, category_id, position) VALUES (%s, (SELECT id FROM categories WHERE name = %s), %d);\n",
				sqlString(t), sqlString(group.category), n)
			n++
		}
	}
	return b.String()
}

// moreCategories is the migration adding Travel and Work (before Other),
// renaming Banking to Banking & Investments, and their tags.
func moreCategories() string {
	var b strings.Builder
	b.WriteString(`UPDATE categories SET position = position + 2 WHERE name = 'Other';
	INSERT OR IGNORE INTO categories (name, position)
		VALUES ('Travel', ifnull((SELECT position - 2 FROM categories WHERE name = 'Other'), (SELECT ifnull(max(position), 0) + 1 FROM categories)));
	INSERT OR IGNORE INTO categories (name, position)
		VALUES ('Work', ifnull((SELECT position - 1 FROM categories WHERE name = 'Other'), (SELECT ifnull(max(position), 0) + 1 FROM categories)));
	UPDATE categories SET name = 'Banking & Investments'
		WHERE name = 'Banking' AND NOT EXISTS (SELECT 1 FROM categories WHERE name = 'Banking & Investments');
	`)
	n := 1000 // after the starting tags
	for _, group := range []struct {
		categories []string
		tags       []string
	}{
		{[]string{"Travel"}, []string{"airline-ticket", "train-ticket", "visa", "hotel-booking"}},
		{[]string{"Work"}, []string{"offer-letter", "payslip", "relieving-letter", "epf"}},
		{[]string{"Banking & Investments", "Banking"}, []string{"mutual-fund", "shares", "ppf", "nps"}},
	} {
		names := make([]string, len(group.categories))
		for i, c := range group.categories {
			names[i] = sqlString(c)
		}
		for _, t := range group.tags {
			fmt.Fprintf(&b, "INSERT OR IGNORE INTO tag_vocab (name, category_id, position) VALUES (%s, (SELECT id FROM categories WHERE name IN (%s) ORDER BY position LIMIT 1), %d);\n",
				sqlString(t), strings.Join(names, ", "), n)
			n++
		}
	}
	return b.String()
}

type VocabTag struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CategoryID int64  `json:"category_id"` // 0: any category
}

func (s *Store) TagVocab(ctx context.Context) ([]VocabTag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT v.id, v.name, ifnull(v.category_id, 0) FROM tag_vocab v
		LEFT JOIN categories c ON c.id = v.category_id ORDER BY ifnull(c.position, 1e9), v.position, v.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VocabTag{}
	for rows.Next() {
		var t VocabTag
		if err := rows.Scan(&t.ID, &t.Name, &t.CategoryID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveTagVocab replaces the vocabulary with tags, in that order.
func (s *Store) SaveTagVocab(ctx context.Context, tags []VocabTag) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM tag_vocab`); err != nil {
		return err
	}
	for i, t := range tags {
		_, err := tx.ExecContext(ctx, `INSERT INTO tag_vocab (name, category_id, position) VALUES (?, ?, ?)`,
			t.Name, categoryArg(t.CategoryID), i)
		if isUnique(err) {
			return fmt.Errorf("%w: %s", ErrConflict, t.Name)
		}
		if err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				return fmt.Errorf("%w: category %s", ErrNotFound, strconv.FormatInt(t.CategoryID, 10))
			}
			return err
		}
	}
	return tx.Commit()
}

// UsedTags are the tags on documents a user can see, most used first: the
// classifier reuses them instead of inventing variants. userID 0 means the
// Family space only.
func (s *Store) UsedTags(ctx context.Context, userID int64) ([]string, error) {
	counts, err := s.counts(ctx, `SELECT t.tag, count(*) FROM document_tags t JOIN documents d ON d.id = t.document_id
		WHERE `+visible+` GROUP BY t.tag ORDER BY count(*) DESC, t.tag LIMIT 300`, userID)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(counts))
	for i, c := range counts {
		out[i] = c.Name
	}
	return out, nil
}

// UnlistedTags are tags on documents the user can see that aren't in the
// vocabulary, for adding to it.
func (s *Store) UnlistedTags(ctx context.Context, userID int64) ([]Count, error) {
	return s.counts(ctx, `SELECT t.tag, count(*) FROM document_tags t JOIN documents d ON d.id = t.document_id
		WHERE `+visible+` AND NOT EXISTS (SELECT 1 FROM tag_vocab v WHERE v.name = t.tag)
		GROUP BY t.tag ORDER BY count(*) DESC, t.tag LIMIT 200`, userID)
}
