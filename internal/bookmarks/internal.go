package bookmarks

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
)

// replaceTags sets a bookmark's tags to exactly the ones given.
func replaceTags(ctx context.Context, tx *sql.Tx, id int64, tags []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM bookmark_tags WHERE bookmark_id = ?", id); err != nil {
		return err
	}

	for _, raw := range normalizeTags(tags) {
		var tagID int64

		// ON CONFLICT DO UPDATE rather than DO NOTHING, because DO NOTHING
		// returns no row and RETURNING would give nothing to scan.
		err := tx.QueryRowContext(ctx,
			`INSERT INTO tags (name) VALUES (?) ON CONFLICT(name) DO UPDATE SET name = excluded.name RETURNING id`,
			raw).Scan(&tagID)
		if err != nil {
			return err
		}

		if _, err = tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO bookmark_tags (bookmark_id, tag_id) VALUES (?, ?)", id, tagID); err != nil {
			return err
		}
	}

	return pruneTags(ctx, tx)
}

// pruneTags deletes tags no bookmark carries any more, so the tag list is the
// tags in use rather than every tag ever typed.
func pruneTags(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx,
		"DELETE FROM tags WHERE id NOT IN (SELECT tag_id FROM bookmark_tags)")

	return err
}

// normalizeTags lowercases, trims, drops empties and de-duplicates, preserving
// the order they were given in.
func normalizeTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))

	var out []string

	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}

		seen[t] = true

		out = append(out, t)
	}

	return out
}

// reindex rewrites one bookmark's row in the full-text index.
//
// Called inside the same transaction as every write. An index updated
// separately is an index that can be wrong, and a search that silently misses
// a bookmark is worse than one that fails.
func reindex(ctx context.Context, tx *sql.Tx, id int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM bookmarks_fts WHERE rowid = ?", id); err != nil {
		return err
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO bookmarks_fts (rowid, title, notes, url, tags)
		SELECT b.id, b.title, b.notes, b.url,
		       COALESCE((SELECT GROUP_CONCAT(t.name, ' ')
		                 FROM bookmark_tags bt JOIN tags t ON t.id = bt.tag_id
		                 WHERE bt.bookmark_id = b.id), '')
		FROM bookmarks b WHERE b.id = ?`, id)

	return err
}

// ftsQuery turns what somebody typed into a safe FTS5 MATCH expression.
//
// User input cannot go into MATCH unescaped: FTS5 has its own syntax, so a
// trailing "AND", a bare "*" or an unbalanced quote is a query error rather
// than a search for those characters. Every token is quoted, which makes it a
// literal, and the last one gets a prefix wildcard so results appear while
// still typing.
func ftsQuery(q string) string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return unicode.IsSpace(r)
	})

	var tokens []string

	for _, f := range fields {
		// Keep letters, digits and the punctuation that occurs inside URLs;
		// drop the rest rather than escaping it, since none of it is
		// meaningful to a search over titles and links.
		cleaned := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) ||
				r == '.' || r == '-' || r == '_' || r == '/' || r == ':' {
				return r
			}

			return -1
		}, f)

		if cleaned == "" {
			continue
		}

		tokens = append(tokens, `"`+cleaned+`"`)
	}

	if len(tokens) == 0 {
		return ""
	}

	// Prefix-match the final token so a search narrows as it is typed.
	tokens[len(tokens)-1] = strings.TrimSuffix(tokens[len(tokens)-1], `"`) + `"*`

	return strings.Join(tokens, " AND ")
}
