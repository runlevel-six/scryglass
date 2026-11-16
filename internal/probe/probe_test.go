package probe_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/probe"
)

func statusFor(t *testing.T, p *probe.Prober, url string) probe.Status {
	t.Helper()

	for _, s := range p.Snapshot() {
		if s.Resource.URL == url {
			return s
		}
	}

	t.Fatalf("no status for %q", url)

	return probe.Status{}
}

func TestStates(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	// A 401 is an answer. Applications that require a login answer / this way
	// and are working perfectly.
	authed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authed.Close()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()

	p := probe.New(probe.Options{Timeout: 2 * time.Second})
	p.SetResources([]catalog.Resource{
		{Name: "Up", URL: "https://up.example", Check: up.URL},
		{Name: "Authed", URL: "https://authed.example", Check: authed.URL},
		{Name: "Broken", URL: "https://broken.example", Check: broken.URL},
		{Name: "Retired", URL: "https://retired.example", Check: broken.URL, Retired: true},
		{Name: "Bookmark", URL: "https://bookmark.example"},
	})

	p.RunOnce(context.Background())

	cases := map[string]probe.State{
		"https://up.example":       probe.StateUp,
		"https://authed.example":   probe.StateUp,
		"https://broken.example":   probe.StateDown,
		"https://retired.example":  probe.StateRetired,
		"https://bookmark.example": probe.StateUnmonitored,
	}

	for url, want := range cases {
		if got := statusFor(t, p, url).State; got != want {
			t.Errorf("%s: state = %q, want %q", url, got, want)
		}
	}
}

// A retired resource is never probed, even though it has a check that would
// fail. Retired means deliberately not running, and a wall of red for things
// you turned off yourself is how a dashboard stops being read.
func TestRetiredIsNeverProbed(t *testing.T) {
	var hits int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{
		{Name: "R", URL: "https://r.example", Check: srv.URL, Retired: true},
	})
	p.RunOnce(context.Background())

	if hits != 0 {
		t.Fatalf("retired resource was probed %d times", hits)
	}
}

// Discovery rebuilds its list every pass. A resource that survived unchanged
// must keep its sparkline, or the history is always empty and the graph is a lie.
func TestHistorySurvivesRediscovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res := []catalog.Resource{{Name: "A", URL: "https://a.example", Check: srv.URL}}

	p := probe.New(probe.Options{})
	p.SetResources(res)
	p.RunOnce(context.Background())
	p.RunOnce(context.Background())

	before := len(statusFor(t, p, "https://a.example").History)
	if before != 2 {
		t.Fatalf("history = %d, want 2", before)
	}

	// Same resource, rediscovered.
	p.SetResources(res)

	if got := len(statusFor(t, p, "https://a.example").History); got != before {
		t.Fatalf("history = %d after rediscovery, want %d", got, before)
	}
}

// A resource that disappears from discovery disappears from the wall.
func TestRemovedResourceIsDropped(t *testing.T) {
	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{
		{Name: "A", URL: "https://a.example"},
		{Name: "B", URL: "https://b.example"},
	})

	p.SetResources([]catalog.Resource{{Name: "A", URL: "https://a.example"}})

	if got := len(p.Snapshot()); got != 1 {
		t.Fatalf("snapshot has %d entries, want 1", got)
	}
}

// Subscribers hear about a state change and not about a steady state, because
// the page only needs redrawing when something actually moved.
func TestSubscribeOnStateChange(t *testing.T) {
	fail := true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{{Name: "A", URL: "https://a.example", Check: srv.URL}})

	ch, cancel := p.Subscribe()
	defer cancel()

	p.RunOnce(context.Background()) // unknown -> down: a change

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected notification on first probe")
	}

	p.RunOnce(context.Background()) // down -> down: no change

	select {
	case <-ch:
		t.Fatal("notified without a state change")
	case <-time.After(100 * time.Millisecond):
	}

	fail = false
	p.RunOnce(context.Background()) // down -> up: a change

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected notification when the state recovered")
	}
}

// Redirects are answers, not something to follow. Following one would probe
// whatever it points at — for a gated application, the login page.
func TestRedirectIsNotFollowed(t *testing.T) {
	var target int

	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		target++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dest.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusFound)
	}))
	defer src.Close()

	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{{Name: "A", URL: "https://a.example", Check: src.URL}})
	p.RunOnce(context.Background())

	if got := statusFor(t, p, "https://a.example").State; got != probe.StateUp {
		t.Errorf("state = %q, want up: a redirect is an answer", got)
	}

	if target != 0 {
		t.Errorf("the redirect was followed %d times", target)
	}
}

func TestUnreachableIsDown(t *testing.T) {
	p := probe.New(probe.Options{Timeout: 200 * time.Millisecond})
	// Reserved TEST-NET-1; nothing answers here.
	p.SetResources([]catalog.Resource{
		{Name: "A", URL: "https://a.example", Check: "http://192.0.2.1:9/"},
	})
	p.RunOnce(context.Background())

	s := statusFor(t, p, "https://a.example")
	if s.State != probe.StateDown {
		t.Fatalf("state = %q, want down", s.State)
	}

	if s.Err == "" {
		t.Error("a down tile should carry why")
	}
}

// Discovery adding a tile is as much a reason to redraw as one changing color.
// Without this a newly deployed application does not appear until something
// unrelated happens to flip.
func TestSetResourcesNotifiesWhenTheSetChanges(t *testing.T) {
	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{{Name: "A", URL: "https://a.example"}})

	ch, cancel := p.Subscribe()
	defer cancel()

	p.SetResources([]catalog.Resource{
		{Name: "A", URL: "https://a.example"},
		{Name: "B", URL: "https://b.example"},
	})

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("adding a resource did not notify")
	}

	// The same set again is not news.
	p.SetResources([]catalog.Resource{
		{Name: "A", URL: "https://a.example"},
		{Name: "B", URL: "https://b.example"},
	})

	select {
	case <-ch:
		t.Fatal("notified when the resource set had not changed")
	case <-time.After(200 * time.Millisecond):
	}
}

// Two resources sharing a URL are one tile, not the same entry drawn twice
// under the last one's name. The first wins.
func TestDuplicateURLIsOneTile(t *testing.T) {
	p := probe.New(probe.Options{})
	p.SetResources([]catalog.Resource{
		{Name: "First", URL: "https://a.example"},
		{Name: "Second", URL: "https://a.example"},
		{Name: "Other", URL: "https://b.example"},
	})

	snap := p.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot has %d entries, want 2", len(snap))
	}

	if snap[0].Resource.Name != "First" {
		t.Errorf("kept %q, want the first", snap[0].Resource.Name)
	}
}
