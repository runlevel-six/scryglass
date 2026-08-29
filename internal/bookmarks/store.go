// Package bookmarks stores the links you keep.
//
// This is the half of Scryglass the cluster cannot supply. A tile is
// discovered; a bookmark is something somebody decided to remember, and the
// only place that decision exists is here.
//
// SQLite, via a pure-Go driver. Not a preference — the image is
// distroless/static with no libc, so a driver needing CGO cannot run in it at
// all. FTS5 is available in that driver, which is what makes search a query
// rather than a scan.
package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // database/sql driver, registered as "sqlite"
)

// ErrNotFound is returned when a bookmark does not exist.
var ErrNotFound = errors.New("bookmark not found")

// Bookmark is a link worth keeping.
type Bookmark struct {
	ID        int64
	URL       string
	Title     string
	Notes     string
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store is the bookmark database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// migrations are applied in order and tracked in SQLite's user_version.
//
// Append only. A migration that has shipped has run against a database
// somebody cares about, and editing it in place means two deployments disagree
// about what schema version 3 means.
var migrations = []string{
	`
	CREATE TABLE bookmarks (
		id         INTEGER PRIMARY KEY,
		url        TEXT NOT NULL UNIQUE,
		title      TEXT NOT NULL,
		notes      TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);

	CREATE TABLE tags (
		id   INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE COLLATE NOCASE
	);

	CREATE TABLE bookmark_tags (
		bookmark_id INTEGER NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
		tag_id      INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
		PRIMARY KEY (bookmark_id, tag_id)
	);

	CREATE INDEX bookmark_tags_tag ON bookmark_tags(tag_id);

	-- A standalone FTS table rather than an external-content one, because tags
	-- live in another table and would not be indexed by content= triggers
	-- without a second mechanism anyway. This is written in the same
	-- transaction as every change, which is the property that matters.
	CREATE VIRTUAL TABLE bookmarks_fts USING fts5(title, notes, url, tags);
	`,
}

// Open opens or creates the database and brings the schema up to date.
func Open(path string) (*Store, error) {
	// Pragmas go in the DSN so they apply to every connection in the pool, not
	// just the first one that happens to run them.
	dsn := path + "?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	// One writer. SQLite allows exactly one at a time and will return
	// SQLITE_BUSY rather than queue; serializing here turns a class of runtime
	// error into a short wait.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, now: time.Now}

	if err := s.migrate(context.Background()); err != nil {
		db.Close()

		return nil, err
	}

	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var version int

	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for i := version; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}

		if _, err = tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()

			return fmt.Errorf("migration %d: %w", i+1, err)
		}

		// user_version does not accept a bound parameter.
		if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()

			return fmt.Errorf("record migration %d: %w", i+1, err)
		}

		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}
	}

	return nil
}

// Save inserts a bookmark, or updates the existing one with the same URL.
//
// Upsert rather than an error on conflict, because the common way to add a
// bookmark is a bookmarklet on a page you are already looking at — and doing
// that twice should correct the entry, not fail. It returns the id either way.
func (s *Store) Save(ctx context.Context, b Bookmark) (int64, error) {
	b.URL = strings.TrimSpace(b.URL)
	if b.URL == "" {
		return 0, errors.New("url is required")
	}

	if b.Title = strings.TrimSpace(b.Title); b.Title == "" {
		b.Title = b.URL
	}

	now := s.now().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	var id int64

	err = tx.QueryRowContext(ctx, `
		INSERT INTO bookmarks (url, title, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET
			title = excluded.title,
			notes = excluded.notes,
			updated_at = excluded.updated_at
		RETURNING id`,
		b.URL, b.Title, b.Notes, now, now).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("save bookmark: %w", err)
	}

	if err = replaceTags(ctx, tx, id, b.Tags); err != nil {
		return 0, err
	}

	if err = reindex(ctx, tx, id); err != nil {
		return 0, err
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}

	return id, nil
}

// Delete removes a bookmark. Deleting something that is not there is not an
// error: the caller wanted it gone, and it is.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if _, err = tx.ExecContext(ctx, "DELETE FROM bookmarks WHERE id = ?", id); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, "DELETE FROM bookmarks_fts WHERE rowid = ?", id); err != nil {
		return err
	}

	if err = pruneTags(ctx, tx); err != nil {
		return err
	}

	return tx.Commit()
}

// Get returns one bookmark by id.
func (s *Store) Get(ctx context.Context, id int64) (Bookmark, error) {
	// GROUP BY is required even for a single row: the projection aggregates
	// tags, so without it a query matching nothing is an aggregate over zero
	// rows — which returns one row full of NULLs rather than no rows at all.
	rows, err := s.db.QueryContext(ctx, selectBookmarks+" WHERE b.id = ? GROUP BY b.id", id)
	if err != nil {
		return Bookmark{}, err
	}
	defer rows.Close()

	out, err := scanBookmarks(rows)
	if err != nil {
		return Bookmark{}, err
	}

	if len(out) == 0 {
		return Bookmark{}, ErrNotFound
	}

	return out[0], nil
}

// FindByURL returns the bookmark with this URL, if there is one.
//
// Used by the add page so that re-adding a page you already saved edits the
// existing entry rather than presenting an empty form whose save would discard
// the notes and tags already on it.
func (s *Store) FindByURL(ctx context.Context, rawURL string) (Bookmark, error) {
	rows, err := s.db.QueryContext(ctx, selectBookmarks+" WHERE b.url = ? GROUP BY b.id", strings.TrimSpace(rawURL))
	if err != nil {
		return Bookmark{}, err
	}
	defer rows.Close()

	out, err := scanBookmarks(rows)
	if err != nil {
		return Bookmark{}, err
	}

	if len(out) == 0 {
		return Bookmark{}, ErrNotFound
	}

	return out[0], nil
}

// selectBookmarks is the shared projection. Tags arrive as a single delimited
// string rather than a second query per row, because the alternative is one
// round trip per bookmark and this is read on every page.
const selectBookmarks = `
	SELECT b.id, b.url, b.title, b.notes, b.created_at, b.updated_at,
	       COALESCE(GROUP_CONCAT(t.name, char(31)), '')
	FROM bookmarks b
	LEFT JOIN bookmark_tags bt ON bt.bookmark_id = b.id
	LEFT JOIN tags t ON t.id = bt.tag_id`

const groupAndOrder = " GROUP BY b.id ORDER BY b.updated_at DESC"

// List returns bookmarks, newest first. An empty tag returns everything.
func (s *Store) List(ctx context.Context, tag string, limit int) ([]Bookmark, error) {
	query := selectBookmarks
	args := []any{}

	if tag = strings.TrimSpace(tag); tag != "" {
		// The filter is a separate join from the projection join, or filtering
		// by one tag would hide every other tag the bookmark carries.
		query += `
			JOIN bookmark_tags ft ON ft.bookmark_id = b.id
			JOIN tags filter ON filter.id = ft.tag_id AND filter.name = ? COLLATE NOCASE`
		args = append(args, tag)
	}

	query += groupAndOrder

	if limit > 0 {
		query += " LIMIT ?"

		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanBookmarks(rows)
}

// Search runs a full-text query, most recently updated first.
func (s *Store) Search(ctx context.Context, q string, limit int) ([]Bookmark, error) {
	match := ftsQuery(q)
	if match == "" {
		return nil, nil
	}

	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, selectBookmarks+`
		JOIN bookmarks_fts f ON f.rowid = b.id AND bookmarks_fts MATCH ?`+
		groupAndOrder+" LIMIT ?", match, limit)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	return scanBookmarks(rows)
}

// TagCount is a tag and how many bookmarks carry it.
type TagCount struct {
	Name  string
	Count int
}

// Tags lists tags in use, most used first.
func (s *Store) Tags(ctx context.Context) ([]TagCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.name, COUNT(bt.bookmark_id)
		FROM tags t JOIN bookmark_tags bt ON bt.tag_id = t.id
		GROUP BY t.id ORDER BY COUNT(bt.bookmark_id) DESC, t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TagCount

	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Name, &tc.Count); err != nil {
			return nil, err
		}

		out = append(out, tc)
	}

	return out, rows.Err()
}

// tagSep is the unit separator, used to join tags in one column.
//
// A character nobody types, deliberately: a comma would make a tag containing
// a comma indistinguishable from two tags.
const tagSep = "\x1f"

func scanBookmarks(rows *sql.Rows) ([]Bookmark, error) {
	var out []Bookmark

	for rows.Next() {
		var (
			b                Bookmark
			created, updated int64
			tags             string
		)

		if err := rows.Scan(&b.ID, &b.URL, &b.Title, &b.Notes, &created, &updated, &tags); err != nil {
			return nil, err
		}

		b.CreatedAt = time.Unix(created, 0).UTC()
		b.UpdatedAt = time.Unix(updated, 0).UTC()

		if tags != "" {
			b.Tags = strings.Split(tags, tagSep)
		}

		out = append(out, b)
	}

	return out, rows.Err()
}
