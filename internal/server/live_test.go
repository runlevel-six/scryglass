package server_test

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/config"
	"github.com/runlevel-six/scryglass/internal/probe"
	"github.com/runlevel-six/scryglass/internal/server"
)

func newLiveServer(t *testing.T, p *probe.Prober) http.Handler {
	t.Helper()

	cat := &catalog.Catalog{Search: catalog.Search{Name: "Web", URL: "https://s.example/?q={query}"}}
	if err := cat.Validate(); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{BaseURL: "https://home.example", Title: "Scryglass"}

	s, err := server.New(cfg, cat, p, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	return s.Handler()
}

func TestTilesFragment(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer up.Close()

	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{
		{Name: "Photos", URL: "https://photos.example", Group: "media", Check: up.URL},
		{Name: "Gone", URL: "https://gone.example", Check: "http://192.0.2.1:9/"},
	})
	p.RunOnce(context.Background())

	rec := httptest.NewRecorder()
	newLiveServer(t, p).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/tiles", nil))

	body := rec.Body.String()

	for _, want := range []string{"Photos", "state-up", "Gone", "state-down", "media"} {
		if !strings.Contains(body, want) {
			t.Errorf("tiles fragment missing %q\n%s", want, body)
		}
	}

	// The fragment is a fragment. Returning a whole document would nest one
	// page inside another when it is swapped in.
	if strings.Contains(body, "<html") || strings.Contains(body, "<style") {
		t.Error("the tiles fragment should not be a whole document")
	}
}

// Without a prober there are no tiles and no event stream, which is M1's
// behavior and has to keep working: this must run outside a cluster.
func TestNoProberDegradesToOmnibox(t *testing.T) {
	h := newLiveServer(t, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "autofocus") {
		t.Error("the omnibox should still render")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/events", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("/events status = %d, want 404 with no prober", rec.Code)
	}
}

func TestEventStreamPushesOnStateChange(t *testing.T) {
	fail := true

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer backend.Close()

	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{{Name: "A", URL: "https://a.example", Check: backend.URL}})
	p.RunOnce(context.Background()) // settle into down

	srv := httptest.NewServer(newLiveServer(t, p))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	// Recovery is a state change, so it must reach the stream.
	go func() {
		time.Sleep(100 * time.Millisecond)

		fail = false

		p.RunOnce(context.Background())
	}()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: tiles") {
			return
		}
	}

	t.Fatal("no tiles event arrived before the stream ended")
}

// A client that connects after the state has already settled must still be
// told what the state is. Without this, a page opened moments before the first
// probe completes shows "unknown" until something unrelated happens to change,
// which reads as a stuck dashboard.
func TestEventStreamSyncsOnConnect(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer backend.Close()

	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{{Name: "A", URL: "https://a.example", Check: backend.URL}})
	p.RunOnce(context.Background()) // settle before anyone is listening

	srv := httptest.NewServer(newLiveServer(t, p))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: tiles") {
			return
		}
	}

	t.Fatal("no event on connect; a late client would never learn the current state")
}

// Tiles open in a new tab. This page is the browser's new-tab page, so
// navigating away consumes the thing you opened to get here — and rel=noopener
// keeps the opened page from reaching back through window.opener.
func TestTilesOpenInANewTab(t *testing.T) {
	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{{Name: "Photos", URL: "https://photos.example"}})

	rec := httptest.NewRecorder()
	newLiveServer(t, p).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/tiles", nil))

	body := rec.Body.String()

	for _, want := range []string{`target="_blank"`, `rel="noopener"`} {
		if !strings.Contains(body, want) {
			t.Errorf("tile link is missing %s\n%s", want, body)
		}
	}
}
