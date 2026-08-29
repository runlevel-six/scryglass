package server_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/config"
	"github.com/runlevel-six/scryglass/internal/server"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()

	cat := &catalog.Catalog{
		Search: catalog.Search{Name: "Web", URL: "https://search.example/?q={query}"},
		Bangs:  []catalog.Bang{{Key: "t", Name: "Archive", URL: "https://archive.example/?q={query}"}},
		Resources: []catalog.Resource{
			{Name: "Photos", URL: "https://photos.example"},
		},
	}

	if err := cat.Validate(); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Addr: ":0", BaseURL: "https://home.example", Title: "Scryglass"}

	s, err := server.New(cfg, cat, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	return s.Handler()
}

func TestSearchRedirects(t *testing.T) {
	h := newTestServer(t)

	cases := []struct{ name, q, want string }{
		{"resource", "photos", "https://photos.example"},
		{"bang", "!t archiving", "https://archive.example/?q=archiving"},
		{"fallthrough", "kubernetes", "https://search.example/?q=kubernetes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			// The query must be encoded here for the same reason a browser
			// encodes it: a raw space is not a legal request target.
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q="+url.QueryEscape(tc.q), nil))

			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", rec.Code)
			}

			if got := rec.Header().Get("Location"); got != tc.want {
				t.Fatalf("Location = %q, want %q", got, tc.want)
			}
		})
	}
}

// An empty query goes back to the box rather than searching for nothing.
func TestEmptySearchReturnsHome(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(t).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=", nil))

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("status = %d, Location = %q; want 303 to /", rec.Code, rec.Header().Get("Location"))
	}
}

// The index must carry an autofocused input and the inlined stylesheet. Both
// are load-bearing for the new-tab budget: a second request for CSS is a
// second round trip before first paint, and without autofocus the page costs
// a click before it costs a keystroke.
func TestIndexIsSelfContained(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(t).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	body := rec.Body.String()

	for _, want := range []string{"autofocus", "<style>", "opensearch.xml", "!t"} {
		if !strings.Contains(body, want) {
			t.Errorf("index does not contain %q", want)
		}
	}

	if strings.Contains(body, "&lt;style") || strings.Contains(body, "ZgotmplZ") {
		t.Error("stylesheet was escaped instead of inlined")
	}
}

// The descriptor must name absolute URLs; a browser cannot register a search
// engine from a relative one.
func TestOpenSearchDescriptor(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(t).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/opensearch.xml", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "https://home.example/search?q={searchTerms}") {
		t.Fatalf("descriptor does not contain an absolute search template:\n%s", body)
	}
}
