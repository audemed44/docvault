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
