package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/runlevel-six/scryglass/internal/bookmarks"
	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/config"
)

// bookmarkData is what the bookmark templates render.
type bookmarkData struct {
	Title     string
	CSS       template.CSS
	Query     string
	Tag       string
	Bookmarks []bookmarks.Bookmark
	Tags      []bookmarks.TagCount

	// Form is the bookmark being added or edited, on the add page.
	Form bookmarks.Bookmark

	// Popup is true when the add page was opened by the bookmarklet, and so
	// should close itself rather than redirect.
	Popup bool

	// BookmarkletHref is the complete href attribute for the bookmarklet.
	//
	// A whole attribute, not a URL, because html/template has two unhelpful
	// behaviors for this one link and only this escapes both: a plain string
	// href beginning javascript: is replaced with #ZgotmplZ, and typing it as
	// template.URL percent-encodes the entire body instead. Emitting the
	// attribute verbatim renders the literal string.
	//
	// An earlier version assigned this from script. That worked, but a link
	// you are meant to DRAG should not depend on script having run — the
	// failure mode is a link that looks right and drags nothing.
	BookmarkletHref template.HTMLAttr

	// BookmarkletText is the same URL as copyable text, for browsers that
	// refuse to create a bookmark from a dragged javascript: link.
	BookmarkletText string
}

// handleBookmarks lists bookmarks, filtered by a search or a tag.
func (s *Server) handleBookmarks(w http.ResponseWriter, r *http.Request) {
	if s.marks == nil {
		http.NotFound(w, r)
		return
	}

	var (
		ctx   = r.Context()
		query = strings.TrimSpace(r.URL.Query().Get("q"))
		tag   = strings.TrimSpace(r.URL.Query().Get("tag"))
		list  []bookmarks.Bookmark
		err   error
	)

	if query != "" {
		list, err = s.marks.Search(ctx, query, 200)
	} else {
		list, err = s.marks.List(ctx, tag, 200)
	}

	if err != nil {
		s.log.Error("list bookmarks", "error", err)
		http.Error(w, "could not read bookmarks", http.StatusInternalServerError)

		return
	}

	tags, err := s.marks.Tags(ctx)
	if err != nil {
		s.log.Error("list tags", "error", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	data := bookmarkData{
		Title:     s.cfg.Title,
		CSS:       s.css,
		Query:     query,
		Tag:       tag,
		Bookmarks: list,
		Tags:      tags,

		//nolint:gosec // G203: built from validated config, never from a request
		BookmarkletHref: template.HTMLAttr(`href="` + html.EscapeString(s.bookmarklet()) + `"`),
		BookmarkletText: s.bookmarklet(),
	}

	if err := s.tmpl.ExecuteTemplate(w, "bookmarks.html", data); err != nil {
		s.log.Error("render bookmarks", "error", err)
	}
}

// handleBookmarkForm renders the add page.
//
// A GET with parameters, opened in a popup, and that shape is deliberate.
// A bookmarklet cannot POST in the background: behind an authenticating proxy
// an expired session answers a cross-origin fetch with a redirect to a login
// page, which fails silently. A normal navigation carries the session cookie
// and can be signed back in.
func (s *Server) handleBookmarkForm(w http.ResponseWriter, r *http.Request) {
	if s.marks == nil {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()

	form := bookmarks.Bookmark{
		URL:   strings.TrimSpace(q.Get("url")),
		Title: strings.TrimSpace(q.Get("title")),
	}

	// Whether this closes itself on save depends on how it was opened, not on
	// the form. Hardcoding it left a manual add staring at a bare "Saved."
	// page, because window.close() does nothing to a tab the script did not
	// open.
	popup := q.Get("popup") != ""

	// Editing an existing bookmark loads it, so the popup is one page for both
	// jobs rather than two that drift apart.
	if id, err := strconv.ParseInt(q.Get("id"), 10, 64); err == nil {
		if existing, err := s.marks.Get(r.Context(), id); err == nil {
			form = existing
		}
	} else if form.URL != "" {
		// Re-adding a URL already saved should edit it, not offer an empty
		// form that silently overwrites the notes and tags on save.
		if existing, err := s.marks.FindByURL(r.Context(), form.URL); err == nil {
			if form.Title == "" {
				form.Title = existing.Title
			}

			existing.Title = form.Title
			form = existing
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	err := s.tmpl.ExecuteTemplate(w, "bookmark_form.html", bookmarkData{
		Title: s.cfg.Title,
		CSS:   s.css,
		Form:  form,
		Popup: popup,
	})
	if err != nil {
		s.log.Error("render bookmark form", "error", err)
	}
}

// handleBookmarkSave creates or updates a bookmark.
func (s *Server) handleBookmarkSave(w http.ResponseWriter, r *http.Request) {
	if s.marks == nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	b := bookmarks.Bookmark{
		URL:   strings.TrimSpace(r.PostFormValue("url")),
		Title: strings.TrimSpace(r.PostFormValue("title")),
		Notes: strings.TrimSpace(r.PostFormValue("notes")),
		Tags:  splitTags(r.PostFormValue("tags")),
	}

	if _, err := s.marks.Save(r.Context(), b); err != nil {
		s.log.Error("save bookmark", "error", err, "url", b.URL)
		http.Error(w, "could not save: "+err.Error(), http.StatusBadRequest)

		return
	}

	// A popup closes itself; a full page goes back to the list. The popup is
	// the bookmarklet's flow and closing it is the whole point — being left
	// looking at a list of every bookmark you own, on top of the page you were
	// reading, is not a result.
	if r.PostFormValue("popup") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		writeAll(w, []byte(`<!doctype html><title>Saved</title>`+
			`<p>Saved.<script>window.close()</script>`), s.log)

		return
	}

	http.Redirect(w, r, "/bookmarks", http.StatusSeeOther)
}

// handleBookmarkDelete removes a bookmark.
func (s *Server) handleBookmarkDelete(w http.ResponseWriter, r *http.Request) {
	if s.marks == nil {
		http.NotFound(w, r)
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}

	if err := s.marks.Delete(r.Context(), id); err != nil && !errors.Is(err, bookmarks.ErrNotFound) {
		s.log.Error("delete bookmark", "error", err, "id", id)
		http.Error(w, "could not delete", http.StatusInternalServerError)

		return
	}

	http.Redirect(w, r, "/bookmarks", http.StatusSeeOther)
}

// bookmarklet is the javascript: URL to drag onto a bookmarks bar.
//
// It opens the add page in a popup rather than posting in the background:
// behind an authenticating proxy an expired session answers a cross-origin
// fetch with a redirect to a login page, which fails silently. A navigation
// carries the session cookie and can be signed back in.
//
// Single quotes throughout, so the string survives being placed in a
// double-quoted HTML attribute.
func (s *Server) bookmarklet() string {
	return "javascript:(function(){window.open('" +
		strings.TrimRight(s.cfg.BaseURL, "/") + "/bookmarks/new?popup=1&url='" +
		"+encodeURIComponent(location.href)+'&title='" +
		"+encodeURIComponent(document.title),'scryglass','width=560,height=460');})();"
}

func splitTags(s string) []string {
	var out []string

	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// withBookmarkBang registers !b automatically when bookmarks are enabled.
//
// Configuring a bang for a feature the same binary serves would be a knob
// whose only correct setting is the default. It is still overridable: a
// catalog that defines its own "b" wins, because someone who wrote one meant
// it.
func withBookmarkBang(cat *catalog.Catalog, cfg *config.Config, enabled bool) *catalog.Catalog {
	if !enabled {
		return cat
	}

	for _, b := range cat.Bangs {
		if strings.EqualFold(b.Key, "b") {
			return cat
		}
	}

	next := *cat
	next.Bangs = append(append([]catalog.Bang{}, cat.Bangs...), catalog.Bang{
		Key:  "b",
		Name: "Bookmarks",
		URL:  strings.TrimRight(cfg.BaseURL, "/") + "/bookmarks?q=" + catalog.Placeholder,
	})

	return &next
}
