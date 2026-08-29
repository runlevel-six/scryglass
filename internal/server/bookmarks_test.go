package server_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/runlevel-six/scryglass/internal/bookmarks"
	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/config"
	"github.com/runlevel-six/scryglass/internal/server"
)

func newBookmarkServer(t *testing.T) (http.Handler, *bookmarks.Store) {
	t.Helper()

	store, err := bookmarks.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { store.Close() })

	cat := &catalog.Catalog{Search: catalog.Search{Name: "Web", URL: "https://s.example/?q={query}"}}
	if err := cat.Validate(); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{BaseURL: "https://home.example", Title: "Scryglass"}

	s, err := server.New(cfg, cat, nil, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	return s.Handler(), store
}

func post(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestSaveListAndDelete(t *testing.T) {
	h, store := newBookmarkServer(t)

	rec := post(t, h, "/bookmarks", url.Values{
		"url": {"https://example.com/a"}, "title": {"A page"}, "tags": {"infra, reading"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, want 303", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bookmarks", nil))

	body := rec.Body.String()
	for _, want := range []string{"A page", "https://example.com/a", "infra", "reading"} {
		if !strings.Contains(body, want) {
			t.Errorf("list is missing %q", want)
		}
	}

	saved, err := store.List(t.Context(), "", 0)
	if err != nil || len(saved) != 1 {
		t.Fatalf("store has %d bookmarks (%v)", len(saved), err)
	}

	rec = post(t, h, "/bookmarks/"+strconv.FormatInt(saved[0].ID, 10)+"/delete", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d", rec.Code)
	}

	if after, _ := store.List(t.Context(), "", 0); len(after) != 0 {
		t.Errorf("bookmark survived deletion")
	}
}

// A popup closes itself. Being left looking at a list of every bookmark you
// own, on top of the page you were reading, is not a result.
func TestPopupSaveClosesItself(t *testing.T) {
	h, _ := newBookmarkServer(t)

	rec := post(t, h, "/bookmarks", url.Values{
		"url": {"https://example.com/a"}, "title": {"A"}, "popup": {"1"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a popup save", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "window.close()") {
		t.Error("the popup does not close itself")
	}
}

// Re-adding a page already saved must edit it, not offer an empty form whose
// save would discard the notes and tags already on it.
func TestAddingAKnownURLPrefillsTheExistingEntry(t *testing.T) {
	h, store := newBookmarkServer(t)

	if _, err := store.Save(t.Context(), bookmarks.Bookmark{
		URL: "https://example.com/a", Title: "Original", Notes: "kept", Tags: []string{"infra"},
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/bookmarks/new?url=https%3A%2F%2Fexample.com%2Fa&title=From+the+page", nil))

	body := rec.Body.String()

	for _, want := range []string{"kept", "infra"} {
		if !strings.Contains(body, want) {
			t.Errorf("form did not prefill %q; saving would discard it\n%s", want, body)
		}
	}

	// The page's current title wins, since that is what the bookmarklet just
	// read off the page you are looking at.
	if !strings.Contains(body, "From the page") {
		t.Error("the title from the page was not offered")
	}
}

// !b is registered without configuration, because a knob whose only correct
// setting is the default is not a knob.
func TestBookmarkBangIsAutomatic(t *testing.T) {
	h, _ := newBookmarkServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=%21b+ceph", nil))

	if got := rec.Header().Get("Location"); got != "https://home.example/bookmarks?q=ceph" {
		t.Fatalf("Location = %q", got)
	}
}

// With no store, the bookmark routes are absent rather than broken — which is
// how this stays runnable with no volume attached.
func TestBookmarksAbsentWithoutAStore(t *testing.T) {
	h := newLiveServer(t, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bookmarks", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 with no store", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=%21b+x", nil))

	// Without bookmarks, !b is not a bang and falls through to web search.
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "https://s.example/") {
		t.Errorf("Location = %q, want the web search fallback", loc)
	}
}

// html/template refuses a javascript: href and substitutes #ZgotmplZ, which is
// right for anything derived from a request and wrong for this one. The
// failure is silent — the page renders, the link simply does nothing — so it
// gets a test.
func TestBookmarkletRendersAsJavascriptURL(t *testing.T) {
	h, _ := newBookmarkServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bookmarks", nil))

	body := rec.Body.String()

	if strings.Contains(body, "ZgotmplZ") {
		t.Fatal("the bookmarklet href was filtered out by html/template")
	}

	if !strings.Contains(body, `id="bookmarklet"`) {
		t.Error("no bookmarklet link in the page")
	}

	if !strings.Contains(body, "javascript:(function()") {
		t.Error("the script does not build a javascript: URL")
	}

	// And it must carry the configured external address, not a relative path:
	// this runs on somebody else's page, where a relative URL means their
	// site. Base and path are concatenated at runtime, so both halves are
	// checked rather than the joined string.
	if !strings.Contains(body, `"https://home.example"`) {
		t.Error("the bookmarklet does not carry the absolute base URL")
	}

	if !strings.Contains(body, "/bookmarks/new?url=") {
		t.Error("the bookmarklet does not target the add page")
	}
}
