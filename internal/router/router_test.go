package router_test

import (
	"testing"

	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/router"
)

func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()

	c := &catalog.Catalog{
		Search: catalog.Search{Name: "Web", URL: "https://search.example/search?q={query}"},
		Bangs: []catalog.Bang{
			{Key: "t", Name: "Archive", URL: "https://archive.example/search?q={query}"},
			{Key: "w", Name: "Wishes", URL: "https://wishes.example/?q={query}"},
		},
		Resources: []catalog.Resource{
			{Name: "Photos", URL: "https://photos.example", Aliases: []string{"gallery", "pics"}},
			{Name: "Plex", URL: "https://media.example"},
			{Name: "Notes", URL: "https://notes.example"},
		},
	}

	if err := c.Validate(); err != nil {
		t.Fatalf("test catalog is invalid: %v", err)
	}

	return c
}

func TestRoute(t *testing.T) {
	r := router.New(testCatalog(t))

	cases := []struct {
		name   string
		in     string
		kind   router.Kind
		target string
	}{
		{"empty", "", router.KindEmpty, ""},
		{"whitespace only", "   ", router.KindEmpty, ""},

		{"exact resource name", "Photos", router.KindResource, "https://photos.example"},
		{"resource name is case-insensitive", "photos", router.KindResource, "https://photos.example"},
		{"resource by alias", "gallery", router.KindResource, "https://photos.example"},
		{"resource by unique prefix", "not", router.KindResource, "https://notes.example"},

		{"leading bang", "!t wal archiving", router.KindBang, "https://archive.example/search?q=wal+archiving"},
		{"trailing bang", "wal archiving !t", router.KindBang, "https://archive.example/search?q=wal+archiving"},
		{"bang key is case-insensitive", "!T archiving", router.KindBang, "https://archive.example/search?q=archiving"},
		{"bang with no query", "!w", router.KindBang, "https://wishes.example/?q="},

		{"unknown bang falls through to search", "!zz thing", router.KindSearch, "https://search.example/search?q=%21zz+thing"},
		{"bare word with no match searches", "kubernetes", router.KindSearch, "https://search.example/search?q=kubernetes"},

		{"query is escaped", "a&b c", router.KindSearch, "https://search.example/search?q=a%26b+c"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Route(tc.in)

			if got.Kind != tc.kind {
				t.Errorf("Route(%q).Kind = %v, want %v", tc.in, got.Kind, tc.kind)
			}

			if got.Target != tc.target {
				t.Errorf("Route(%q).Target = %q, want %q", tc.in, got.Target, tc.target)
			}
		})
	}
}

// A bang beats a resource that happens to share the name. Explicit intent is
// not overridden by inference.
func TestBangBeatsResource(t *testing.T) {
	c := testCatalog(t)
	c.Resources = append(c.Resources, catalog.Resource{Name: "t", URL: "https://letter-t.example"})

	got := router.New(c).Route("!t something")
	if got.Kind != router.KindBang {
		t.Fatalf("Kind = %v, want KindBang", got.Kind)
	}
}

// Ambiguity must not guess. Two resources matching equally well means the
// query goes to search, because a confident jump to the wrong application
// looks deliberate and is worse than showing results.
func TestAmbiguousPrefixDoesNotGuess(t *testing.T) {
	c := testCatalog(t)
	c.Resources = append(c.Resources, catalog.Resource{Name: "Photoprism", URL: "https://prism.example"})

	got := router.New(c).Route("photo")
	if got.Kind != router.KindSearch {
		t.Fatalf("Route(%q).Kind = %v, want KindSearch (target %q)", "photo", got.Kind, got.Target)
	}
}

// An exact name beats a prefix match on a different resource, even though the
// prefix candidate is also valid. Tiers win outright.
func TestExactBeatsPrefixOnAnother(t *testing.T) {
	c := testCatalog(t)
	c.Resources = append(c.Resources, catalog.Resource{Name: "Notesmith", URL: "https://smith.example"})

	got := router.New(c).Route("notes")
	if got.Kind != router.KindResource || got.Target != "https://notes.example" {
		t.Fatalf("Route(notes) = %+v, want the exact match", got)
	}
}

// A resource matching on both its name and one of its aliases is one
// candidate, not two — otherwise it would look ambiguous to itself.
func TestNameAndAliasIsOneCandidate(t *testing.T) {
	c := testCatalog(t)
	c.Resources = []catalog.Resource{
		{Name: "Photos", URL: "https://photos.example", Aliases: []string{"photos-gallery"}},
	}

	got := router.New(c).Route("photos")
	if got.Kind != router.KindResource {
		t.Fatalf("Kind = %v, want KindResource", got.Kind)
	}
}
