// Package server is the HTTP layer.
//
// It is deliberately thin. Every routing decision belongs to internal/router,
// which is pure and tested without a server; this package turns a Decision
// into a redirect and renders one page.
package server

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/config"
	"github.com/runlevel-six/scryglass/internal/router"
)

//go:embed templates
var templatesFS embed.FS

// Server serves the omnibox.
type Server struct {
	cfg  *config.Config
	cat  *catalog.Catalog
	rtr  *router.Router
	log  *slog.Logger
	tmpl *template.Template

	// page is the fully rendered index, built once at startup.
	//
	// This is a new-tab page and the budget is one paint. Nothing on it
	// depends on the request, so rendering per request would be work done
	// thousands of times to produce identical bytes.
	page []byte

	// descriptor is the OpenSearch document, likewise built once.
	descriptor []byte
}

// New builds a Server and pre-renders everything that does not vary per request.
func New(cfg *config.Config, cat *catalog.Catalog, log *slog.Logger) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/index.html", "templates/opensearch.xml")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	css, err := templatesFS.ReadFile("templates/style.css")
	if err != nil {
		return nil, fmt.Errorf("read stylesheet: %w", err)
	}

	s := &Server{cfg: cfg, cat: cat, rtr: router.New(cat), log: log, tmpl: tmpl}

	var page, descriptor strings.Builder

	err = tmpl.ExecuteTemplate(&page, "index.html", struct {
		Title      string
		SearchName string
		CSS        template.CSS
		Bangs      []catalog.Bang
	}{
		Title:      cfg.Title,
		SearchName: labelOr(cat.Search.Name, "the web"),
		// The stylesheet is ours, embedded from templates/ at build time and
		// never derived from a request; without this conversion it would be
		// escaped into unusable text inside the <style> element. gosec cannot
		// see where the bytes came from, only that the conversion bypasses
		// escaping — which is exactly what is intended here.
		CSS:   template.CSS(css), //nolint:gosec // G203: embedded asset, not user input
		Bangs: cat.Bangs,
	})
	if err != nil {
		return nil, fmt.Errorf("render index: %w", err)
	}

	err = tmpl.ExecuteTemplate(&descriptor, "opensearch.xml", struct {
		Title   string
		BaseURL string
	}{Title: cfg.Title, BaseURL: cfg.BaseURL})
	if err != nil {
		return nil, fmt.Errorf("render opensearch descriptor: %w", err)
	}

	s.page = []byte(page.String())
	s.descriptor = []byte(descriptor.String())

	return s, nil
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /opensearch.xml", s.handleDescriptor)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleHealth)

	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// No caching. The page is cheap to produce and, from M2, its contents
	// change when the cluster does; a stale new-tab page showing a service
	// that no longer exists is the failure this project set out to end.
	w.Header().Set("Cache-Control", "no-store")
	writeAll(w, s.page, s.log)
}

// handleSearch is the omnibox. Everything arrives here and leaves as a redirect.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	d := s.rtr.Route(r.URL.Query().Get("q"))

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
