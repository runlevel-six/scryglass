// Package server is the HTTP layer.
//
// It is deliberately thin. Every routing decision belongs to internal/router,
// which is pure and tested without a server; this package turns a Decision
// into a redirect, renders one page, and pushes a notification when a tile
// changes state.
package server

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/runlevel-six/scryglass/internal/bookmarks"
	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/config"
	"github.com/runlevel-six/scryglass/internal/probe"
	"github.com/runlevel-six/scryglass/internal/router"
)

//go:embed templates
var templatesFS embed.FS

// Server serves the omnibox and the tile wall.
type Server struct {
	cfg    *config.Config
	log    *slog.Logger
	tmpl   *template.Template
	prober *probe.Prober
	marks  *bookmarks.Store
	now    func() time.Time

	css template.CSS
	js  template.JS

	// descriptor is the OpenSearch document. It is the one thing still built
	// once, because nothing in it can change while the process runs.
	descriptor []byte

	mu  sync.RWMutex
	cat *catalog.Catalog
	rtr *router.Router
}

// New builds a Server.
//
// The prober may be nil, in which case no tiles are rendered and no events are
// pushed — which is exactly M1's behavior, and worth keeping reachable.
func New(
	cfg *config.Config,
	cat *catalog.Catalog,
	prober *probe.Prober,
	marks *bookmarks.Store,
	log *slog.Logger,
) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS,
		"templates/index.html", "templates/tiles.html", "templates/opensearch.xml",
		"templates/bookmarks.html", "templates/bookmark_form.html",
		"templates/head.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	css, err := templatesFS.ReadFile("templates/style.css")
	if err != nil {
		return nil, fmt.Errorf("read stylesheet: %w", err)
	}

	js, err := templatesFS.ReadFile("templates/live.js")
	if err != nil {
		return nil, fmt.Errorf("read script: %w", err)
	}

	s := &Server{
		cfg:    cfg,
		log:    log,
		tmpl:   tmpl,
		prober: prober,
		marks:  marks,
		now:    time.Now,
		// Both are ours, embedded at build time and never derived from a
		// request; without these conversions they would be escaped into
		// unusable text inside their elements.
		css: template.CSS(css), //nolint:gosec // G203: embedded asset, not user input
		js:  template.JS(js),   //nolint:gosec // G203: embedded asset, not user input
		cat: withBookmarkBang(cat, cfg, marks != nil),
	}

	s.rtr = router.New(s.cat)

	var descriptor strings.Builder

	err = tmpl.ExecuteTemplate(&descriptor, "opensearch.xml", struct {
		Title   string
		BaseURL string
	}{Title: cfg.Title, BaseURL: cfg.BaseURL})
	if err != nil {
		return nil, fmt.Errorf("render opensearch descriptor: %w", err)
	}

	s.descriptor = []byte(descriptor.String())

	return s, nil
}

// SetResources replaces the routable resources and rebuilds the router.
//
// Called by the discovery loop on every pass. The search and bang
// configuration is untouched: those come from the file and the cluster has no
// opinion about them.
func (s *Server) SetResources(resources []catalog.Resource) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := *s.cat
	next.Resources = resources

	s.cat = &next
	s.rtr = router.New(&next)
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /tiles", s.handleTiles)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /bookmarks", s.handleBookmarks)
	mux.HandleFunc("GET /bookmarks/new", s.handleBookmarkForm)
	mux.HandleFunc("POST /bookmarks", s.handleBookmarkSave)
	mux.HandleFunc("POST /bookmarks/{id}/delete", s.handleBookmarkDelete)
	mux.HandleFunc("GET /assets/{version}/{file}", s.handleAsset)
	mux.HandleFunc("GET /favicon.ico", s.handleFavicon)
	mux.HandleFunc("GET /opensearch.xml", s.handleDescriptor)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleHealth)

	return mux
}

// pageData is everything the index template needs.
type pageData struct {
	Title      string
	AssetBase  string
	User       string
	LogoutURL  string
	SearchName string
	CSS        template.CSS
	JS         template.JS
	Bangs      []catalog.Bang
	Groups     []group
}

// identity is who the authenticating proxy says is asking.
//
// Cosmetic only, and it must stay that way. These headers are set by the proxy
// in front of this, but the Service behind it is reachable by anything in the
// cluster, so a header is a claim rather than a fact. Nothing here authorizes
// on it — Scryglass has no authorization to do — and nothing should start.
func identity(r *http.Request) string {
	for _, h := range []string{"Remote-Name", "Remote-User"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}

	return ""
}

func (s *Server) data(r *http.Request) pageData {
	s.mu.RLock()
	cat := s.cat
	s.mu.RUnlock()

	var groups []group
	if s.prober != nil {
		groups = groupsFrom(s.prober.Snapshot(), s.now(), s.cfg.ProbeInterval, s.cfg.BarWindow)
	}

	return pageData{
		Title:      s.cfg.Title,
		AssetBase:  assetBase(),
		User:       identity(r),
		LogoutURL:  s.cfg.LogoutURL,
		SearchName: labelOr(cat.Search.Name, "the web"),
		CSS:        s.css,
		JS:         s.js,
		Bangs:      cat.Bangs,
		Groups:     groups,
	}
}

// handleIndex renders the page.
//
// Rendered per request rather than cached, now that it carries live state.
// An earlier version served pre-rendered bytes to protect the new-tab budget;
// that budget is spent on round trips, not on executing a template over a few
// dozen tiles, and a cache here would only be somewhere for stale state to
// hide.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if err := s.tmpl.ExecuteTemplate(w, "index.html", s.data(r)); err != nil {
		s.log.Error("render index", "error", err)
	}
}

// handleTiles renders just the wall, for the live update to swap in.
func (s *Server) handleTiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if err := s.tmpl.ExecuteTemplate(w, "tiles", s.data(r)); err != nil {
		s.log.Error("render tiles", "error", err)
	}
}

// handleEvents streams a notification whenever a tile changes state.
//
// The event carries no payload. The client fetches /tiles instead, which keeps
// one renderer rather than two and means a client that reconnects after
// missing events gets current state rather than a replay it cannot apply.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.prober == nil {
		http.Error(w, "no prober", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Buffering proxies will hold an event stream until the response ends,
	// which for a stream is never. nginx honors this hint.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, cancel := s.prober.Subscribe()
	defer cancel()

	// Sync on connect, always.
	//
	// This is the bug that made tiles look stuck: a client that connects after
	// a state change has already happened hears nothing further until the next
	// one, so a page opened moments before the first probe completes sits
	// showing "unknown" until something else moves. Sending one event
	// immediately costs a single fetch and makes every new connection
	// self-correcting.
	fmt.Fprint(w, "event: tiles\ndata: 1\n\n")
	flusher.Flush()

	// A heartbeat, so that an idle stream is not mistaken for a dead one by
	// anything between here and the browser. A comment frame is valid SSE and
	// the client ignores it.
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case _, open := <-events:
			if !open {
				return
			}

			fmt.Fprint(w, "event: tiles\ndata: 1\n\n")
			flusher.Flush()
		}
	}
}

// handleSearch is the omnibox. Everything arrives here and leaves as a redirect.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	rtr := s.rtr
	s.mu.RUnlock()

	d := rtr.Route(r.URL.Query().Get("q"))

	if d.Kind == router.KindEmpty {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	s.log.Debug("routed", "kind", d.Kind, "label", d.Label)

	// 302 rather than 301: a permanent redirect would be cached by the browser
	// against the *query URL*, so editing the catalog would not change where an
	// already-typed query goes.
	http.Redirect(w, r, d.Target, http.StatusFound)
}

func (s *Server) handleDescriptor(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/opensearchdescription+xml")
	writeAll(w, s.descriptor, s.log)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writeAll(w, []byte("ok\n"), s.log)
}

// writeAll logs a failed write rather than discarding the error.
//
// There is nothing to do about it — the response is already committed — but a
// silent io failure is how a proxy problem stays invisible.
func writeAll(w http.ResponseWriter, b []byte, log *slog.Logger) {
	if _, err := w.Write(b); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		log.Debug("response write failed", "error", err)
	}
}

func labelOr(s, fallback string) string {
	if s == "" {
		return fallback
	}

	return s
}
