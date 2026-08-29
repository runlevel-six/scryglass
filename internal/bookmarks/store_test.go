package bookmarks_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/runlevel-six/scryglass/internal/bookmarks"
)

func open(t *testing.T) *bookmarks.Store {
	t.Helper()

	s, err := bookmarks.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { s.Close() })

	return s
}

func TestSaveAndGet(t *testing.T) {
	ctx := t.Context()
	s := open(t)

	id, err := s.Save(ctx, bookmarks.Bookmark{
		URL: "https://example.com/a", Title: "A page", Notes: "worth keeping",
		Tags: []string{"Infra", "reading"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	if got.Title != "A page" || got.Notes != "worth keeping" {
		t.Errorf("got %+v", got)
	}

	// Tags are normalized to lower case so "Infra" and "infra" are one tag.
	if len(got.Tags) != 2 || got.Tags[0] != "infra" {
		t.Errorf("Tags = %v, want lowercased", got.Tags)
	}
}

// Saving the same URL twice corrects the entry rather than failing. The common
// way to add a bookmark is a bookmarklet on a page you are already looking at,
// and doing that twice should not be an error.
func TestSaveIsUpsertOnURL(t *testing.T) {
	ctx := t.Context()
	s := open(t)

	first, err := s.Save(ctx, bookmarks.Bookmark{URL: "https://example.com/a", Title: "First"})
	if err != nil {
		t.Fatal(err)
	}

	second, err := s.Save(ctx, bookmarks.Bookmark{URL: "https://example.com/a", Title: "Second", Tags: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}

	if first != second {
		t.Fatalf("ids %d and %d differ; the second save created a duplicate", first, second)
	}

	got, _ := s.Get(ctx, first)
	if got.Title != "Second" || len(got.Tags) != 1 {
		t.Errorf("got %+v, want the second save to have replaced it", got)
	}
}

// A URL with no title is still a usable bookmark: the link is the point.
func TestTitleDefaultsToURL(t *testing.T) {
	ctx := t.Context()
	s := open(t)

	id, err := s.Save(ctx, bookmarks.Bookmark{URL: "https://example.com/a"})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get(ctx, id)
	if got.Title != "https://example.com/a" {
		t.Errorf("Title = %q", got.Title)
	}
}

func TestSearch(t *testing.T) {
	ctx := t.Context()
	s := open(t)

	seed := []bookmarks.Bookmark{
		{URL: "https://example.com/1", Title: "Placement groups explained", Tags: []string{"storage"}},
		{URL: "https://example.com/2", Title: "Go modules and the proxy", Notes: "vendoring"},
		{URL: "https://example.com/3", Title: "Unrelated", Tags: []string{"misc"}},
	}
	for _, b := range seed {
		if _, err := s.Save(ctx, b); err != nil {
			t.Fatal(err)
		}
	}

	cases := map[string]int{
		"placement": 1,
		"vendoring": 1, // notes are indexed
		"storage":   1, // so are tags
		"example":   3, // and the URL
		"plac":      1, // the last token is a prefix, so it matches while typing
		"nothing":   0,
	}

	for q, want := range cases {
		got, err := s.Search(ctx, q, 0)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}

		if len(got) != want {
			t.Errorf("Search(%q) returned %d, want %d", q, len(got), want)
		}
	}
}

// FTS5 has its own query syntax, so unescaped input is a query error rather
// than a search. None of these may return an error.
func TestSearchSurvivesHostileInput(t *testing.T) {
	ctx := t.Context()
	s := open(t)

	if _, err := s.Save(ctx, bookmarks.Bookmark{URL: "https://example.com/1", Title: "Something"}); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{`AND`, `"`, `*`, `foo AND`, `OR OR`, `(unbalanced`, `NEAR/`, ``, `   `, `^`} {
		if _, err := s.Search(ctx, q, 0); err != nil {
			t.Errorf("Search(%q) errored: %v", q, err)
		}
	}
}

// The index is written in the same transaction as the bookmark. An edit that
// left the index behind would silently stop matching.
func TestSearchFollowsEdits(t *testing.T) {
	ctx := t.Context()
	s := open(t)

	id, err := s.Save(ctx, bookmarks.Bookmark{URL: "https://example.com/1", Title: "Original title"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.Save(ctx, bookmarks.Bookmark{URL: "https://example.com/1", Title: "Replaced title"}); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.Search(ctx, "original", 0); len(got) != 0 {
		t.Errorf("the old title still matches: %+v", got)
	}

	if got, _ := s.Search(ctx, "replaced", 0); len(got) != 1 {
		t.Errorf("the new title does not match")
	}

	if err = s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.Search(ctx, "replaced", 0); len(got) != 0 {
		t.Errorf("a deleted bookmark still matches")
	}
}

func TestListByTag(t *testing.T) {
	ctx := t.Context()
	s := open(t)

	for _, b := range []bookmarks.Bookmark{
		{URL: "https://example.com/1", Title: "One", Tags: []string{"infra", "reading"}},
		{URL: "https://example.com/2", Title: "Two", Tags: []string{"reading"}},
		{URL: "https://example.com/3", Title: "Three"},
	} {
		if _, err := s.Save(ctx, b); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(all) != 3 {
		t.Fatalf("List all returned %d, want 3", len(all))
	}

	reading, err := s.List(ctx, "reading", 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(reading) != 2 {
		t.Fatalf("List by tag returned %d, want 2", len(reading))
	}

	// Filtering by one tag must not hide the others a bookmark carries.
	for _, b := range reading {
		if b.Title == "One" && len(b.Tags) != 2 {
			t.Errorf("filtering by a tag dropped the others: %+v", b)
		}
	}
}

// A tag nothing carries any more is not a tag. Otherwise the list becomes
// every tag ever typed, including the typos.
func TestTagsArePrunedWhenUnused(t *testing.T) {
	ctx := t.Context()
	s := open(t)

	id, err := s.Save(ctx, bookmarks.Bookmark{URL: "https://example.com/1", Title: "One", Tags: []string{"temporary"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.Save(ctx, bookmarks.Bookmark{URL: "https://example.com/1", Title: "One"}); err != nil {
		t.Fatal(err)
	}

	tags, err := s.Tags(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(tags) != 0 {
		t.Errorf("Tags = %+v, want none after the last use was removed", tags)
	}

	_ = id
}

func TestGetMissing(t *testing.T) {
	if _, err := open(t).Get(t.Context(), 999); !errors.Is(err, bookmarks.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	if err := open(t).Delete(context.Background(), 999); err != nil {
		t.Fatalf("deleting a missing bookmark errored: %v", err)
	}
}

// Reopening must not re-run migrations or lose data.
func TestReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	s, err := bookmarks.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.Save(t.Context(), bookmarks.Bookmark{URL: "https://example.com/1", Title: "Kept"}); err != nil {
		t.Fatal(err)
	}

	s.Close()

	again, err := bookmarks.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()

	got, err := again.List(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Title != "Kept" {
		t.Errorf("got %+v after reopen", got)
	}
}
