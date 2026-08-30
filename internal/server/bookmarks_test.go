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

// The bookmarklet is a real href in the served HTML, not something script
// assigns. html/template has two unhelpful behaviors for a javascript: URL —
// replacing it with #ZgotmplZ, or percent-encoding the whole body — and a link
// meant to be DRAGGED must not depend on script having run. Every one of these
// failures renders a page that looks correct and drags nothing.
func TestBookmarkletIsARealHref(t *testing.T) {
	h, _ := newBookmarkServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bookmarks", nil))

	body := rec.Body.String()

	if strings.Contains(body, "ZgotmplZ") {
		t.Fatal("the href was filtered out by html/template")
	}

	if !strings.Contains(body, `href="javascript:(function()`) {
		t.Fatalf("no literal javascript: href in the page")
	}

	if strings.Contains(body, "%28function") {
		t.Error("the href was percent-encoded")
	}

	// It must carry the configured external address: this runs on somebody
	// else's page, where a relative URL means their site.
	if !strings.Contains(body, "https://home.example/bookmarks/new") {
		t.Error("the bookmarklet does not use the absolute base URL")
	}

	// And it must ask for the popup flow, or saving leaves a window that
	// cannot close itself.
	if !strings.Contains(body, "popup=1") {
		t.Error("the bookmarklet does not request the popup flow")
	}
}

// Opened by hand rather than by the bookmarklet, the form must not claim to be
// a popup: window.close() does nothing to a tab the script did not open, so
// saving would leave a bare "Saved." page and no way back.
func TestManualAddIsNotAPopup(t *testing.T) {
	h, _ := newBookmarkServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bookmarks/new", nil))

	if strings.Contains(rec.Body.String(), `name="popup"`) {
		t.Error("a hand-opened form declares itself a popup")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bookmarks/new?popup=1", nil))

	if !strings.Contains(rec.Body.String(), `name="popup"`) {
		t.Error("a form opened as a popup does not declare it")
	}
}

// And a save without the popup flag goes back to the list rather than to a
// page whose only content is the word Saved.
func TestManualSaveRedirectsToTheList(t *testing.T) {
	h, _ := newBookmarkServer(t)

	rec := post(t, h, "/bookmarks", url.Values{"url": {"https://example.com/a"}, "title": {"A"}})

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/bookmarks" {
		t.Fatalf("status = %d, Location = %q; want 303 to /bookmarks", rec.Code, rec.Header().Get("Location"))
	}
}

// Firefox refuses to create a bookmark from a dragged javascript: link, and
// does so silently — the drag simply produces nothing. The page has to say so,
// because the alternative is somebody concluding the feature is broken when
// the link itself works perfectly on click.
func TestPageExplainsTheFirefoxLimitation(t *testing.T) {
	h, _ := newBookmarkServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bookmarks", nil))

	body := rec.Body.String()

	for _, want := range []string{"Firefox", "Add Bookmark", `class="copyable"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not explain the drag limitation: missing %q", want)
		}
	}
}

// Assets are served from an allowlist built at startup, so a request never
// names a file path. Reading a file named by a request is how traversal
// happens, and a name that is not in the map is simply not a file here.
func TestAssetsAreServedFromAnAllowlist(t *testing.T) {
	h, _ := newBookmarkServer(t)

	for _, name := range []string{"mark.png", "icon-32.png", "icon-180.png"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/dev/"+name, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d", name, rec.Code)
		}

		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s: Content-Type = %q", name, ct)
		}

		// Immutable, because the version is in the path.
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("%s: Cache-Control = %q", name, cc)
		}
	}

	for _, bad := range []string{"../../etc/passwd", "..%2f..%2fgo.mod", "nope.png", ""} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/dev/"+bad, nil))

		if rec.Code == http.StatusOK {
			t.Errorf("%q was served", bad)
		}
	}
}

// Browsers request /favicon.ico whether or not a page links one, always at
// that path — so it cannot be versioned, and must not be cached for a year.
func TestFaviconIsServedUnversioned(t *testing.T) {
	h, _ := newBookmarkServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/favicon.ico", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	if cc := rec.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q; this URL is stable across releases and must not be immutable", cc)
	}
}
