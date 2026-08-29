package server

import (
	"errors"
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

	// BaseURL is the external address, used to build the bookmarklet.
	//
	// The bookmarklet's href is assigned by script rather than rendered into
	// the template, and that is not a workaround for the escaper being wrong.
	// html/template refuses a javascript: href outright, and typing it as
	// template.URL only downgrades that to percent-encoding the whole body —
	// which browsers mostly decode and are increasingly willing not to.
	// Assigning .href from script produces the literal string every time.
	BaseURL string
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
		BaseURL:   s.cfg.BaseURL,
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
