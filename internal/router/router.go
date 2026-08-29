// Package router turns what somebody typed into somewhere to go.
//
// This is the whole point of Scryglass and it is deliberately the first thing
// built. A tile wall is available off the shelf; a box that searches your own
// things before it searches the web is not.
//
// Everything here is pure: a Catalog in, a Decision out, no I/O and no clock.
// The HTTP layer turns a Decision into a redirect. That split is what makes
// the interesting behavior — matching, ambiguity, precedence — testable
// without a server.
package router

import (
	"net/url"
	"strings"

	"github.com/runlevel-six/scryglass/internal/catalog"
)

// Kind is what the router decided the input was.
type Kind int

const (
	// KindEmpty means there was nothing to route.
	KindEmpty Kind = iota

	// KindResource means the input named one of your own things.
	KindResource

	// KindBang means the input started or ended with a configured bang.
	KindBang

	// KindSearch means nothing claimed it, so it goes to the web.
	KindSearch
)

// Decision is where a query is going and why.
//
// The why is carried because it is worth logging and, later, worth showing:
// "jumped to Photos" and "searched the web for photos" are very different
// outcomes for the same four keystrokes.
type Decision struct {
	Kind Kind

	// Target is the absolute URL to redirect to. Empty when Kind is KindEmpty.
	Target string

	// Label names what matched — the resource, the bang, or the search engine.
	Label string
}

// Router routes queries against a catalog.
type Router struct {
	cat *catalog.Catalog

	// bangs is the catalog's bang list indexed by lowercased key.
	bangs map[string]catalog.Bang
}

// New builds a Router. The catalog must already be valid; see catalog.Validate.
func New(c *catalog.Catalog) *Router {
	bangs := make(map[string]catalog.Bang, len(c.Bangs))
	for _, b := range c.Bangs {
		bangs[strings.ToLower(b.Key)] = b
	}

	return &Router{cat: c, bangs: bangs}
}

// Route decides where a query goes.
//
// Precedence, highest first:
//
//  1. A bang, leading or trailing. Explicit intent beats inference — if you
//     typed !t you want the archive, even if a resource is called "t".
//  2. A resource, if exactly one matches at the best available tier.
//  3. The web search engine.
//
// Ambiguity falls through to search rather than guessing. Jumping to the wrong
// application is worse than showing search results, because the wrong jump
// looks deliberate.
func (r *Router) Route(q string) Decision {
	q = strings.TrimSpace(q)
	if q == "" {
		return Decision{Kind: KindEmpty}
	}

	if b, rest, ok := r.splitBang(q); ok {
		return Decision{
			Kind:   KindBang,
			Target: expand(b.URL, rest),
			Label:  labelOr(b.Name, b.Key),
		}
	}

	if res, ok := r.matchResource(q); ok {
		return Decision{Kind: KindResource, Target: res.URL, Label: res.Name}
	}

	return Decision{
		Kind:   KindSearch,
		Target: expand(r.cat.Search.URL, q),
		Label:  labelOr(r.cat.Search.Name, "search"),
	}
}

// splitBang finds a bang at either end of the input and returns it with the
// rest of the query.
//
// Both ends are supported because both get typed. You reach for the bang
// first when you know where you are going, and last when you started typing
// and then remembered.
func (r *Router) splitBang(q string) (catalog.Bang, string, bool) {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return catalog.Bang{}, "", false
	}

	if b, ok := r.lookupBang(fields[0]); ok {
		return b, strings.Join(fields[1:], " "), true
	}

	if last := len(fields) - 1; last > 0 {
		if b, ok := r.lookupBang(fields[last]); ok {
			return b, strings.Join(fields[:last], " "), true
		}
	}

	return catalog.Bang{}, "", false
}

func (r *Router) lookupBang(tok string) (catalog.Bang, bool) {
	if !strings.HasPrefix(tok, "!") || len(tok) == 1 {
		return catalog.Bang{}, false
	}

	b, ok := r.bangs[strings.ToLower(tok[1:])]

	return b, ok
}

// matchResource finds the single best resource for a query, or reports that
// there is not exactly one.
//
// Tiers are tried in order and the first tier with any match wins outright: an
// exact name is never beaten by a prefix elsewhere. Within the winning tier,
// more than one candidate means ambiguity, and ambiguity is not resolved by
// guessing.
func (r *Router) matchResource(q string) (catalog.Resource, bool) {
	needle := strings.ToLower(q)

	tiers := []func(name string) bool{
		func(name string) bool { return name == needle },
		func(name string) bool { return strings.HasPrefix(name, needle) },
		func(name string) bool { return strings.Contains(name, needle) },
	}

	for _, match := range tiers {
		var (
			found catalog.Resource
			n     int
		)

		for _, res := range r.cat.Resources {
			if anyMatch(match, res) {
				// The same resource matching on both its name and an alias is
				// one candidate, not two.
				if n == 0 || found.URL != res.URL {
					n++
				}

				found = res
			}
		}

		if n == 1 {
			return found, true
		}

		if n > 1 {
			return catalog.Resource{}, false
		}
	}

	return catalog.Resource{}, false
}

func anyMatch(match func(string) bool, res catalog.Resource) bool {
	if match(strings.ToLower(res.Name)) {
		return true
	}

	for _, a := range res.Aliases {
		if match(strings.ToLower(a)) {
			return true
		}
	}

	return false
}

// expand substitutes the query into a URL template.
//
// QueryEscape rather than PathEscape: every template this is used with puts
// the placeholder in a query string, where a space must become '+' and '&'
// must not survive as a separator. catalog.Validate is what guarantees the
// placeholder is present.
func expand(tmpl, q string) string {
	return strings.ReplaceAll(tmpl, catalog.Placeholder, url.QueryEscape(q))
}

func labelOr(s, fallback string) string {
	if s == "" {
		return fallback
	}

	return s
}
