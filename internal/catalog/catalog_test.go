package catalog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runlevel-six/scryglass/internal/catalog"
)

func write(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

const minimal = `
search:
  name: Web
  url: "https://search.example/?q={query}"
resources:
  - name: Photos
    url: https://photos.example
`

func TestLoad(t *testing.T) {
	c, err := catalog.Load(write(t, minimal))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(c.Resources) != 1 || c.Resources[0].Name != "Photos" {
		t.Fatalf("resources = %+v", c.Resources)
	}
}

// A misspelled key must fail loudly. Silently ignoring it means a resource
// that never appears and a config that looks correct.
func TestUnknownFieldIsRejected(t *testing.T) {
	_, err := catalog.Load(write(t, minimal+"  aliass: [x]\n"))
	if err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		cat  catalog.Catalog
		want string
	}{
		{
			name: "search url is required",
			cat:  catalog.Catalog{},
			want: "search.url is required",
		},
		{
			name: "search url needs the placeholder",
			cat:  catalog.Catalog{Search: catalog.Search{URL: "https://search.example/"}},
			want: "must contain {query}",
		},
		{
			name: "bang key must not include the bang",
			cat: catalog.Catalog{
				Search: catalog.Search{URL: "https://s.example/?q={query}"},
				Bangs:  []catalog.Bang{{Key: "!t", URL: "https://a.example/?q={query}"}},
			},
			want: "written without its leading exclamation mark",
		},
		{
			name: "duplicate bang keys are rejected",
			cat: catalog.Catalog{
				Search: catalog.Search{URL: "https://s.example/?q={query}"},
				Bangs: []catalog.Bang{
					{Key: "t", URL: "https://a.example/?q={query}"},
					{Key: "T", URL: "https://b.example/?q={query}"},
				},
			},
			want: "duplicate key",
		},
		{
			name: "resource needs a url",
			cat: catalog.Catalog{
				Search:    catalog.Search{URL: "https://s.example/?q={query}"},
				Resources: []catalog.Resource{{Name: "Photos"}},
			},
			want: "url is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cat.Validate()
			if err == nil {
				t.Fatal("expected an error")
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	file := []catalog.Resource{
		{Name: "Search", URL: "https://search.example", Aliases: []string{"web"}},
	}
	discovered := []catalog.Resource{
		{Name: "Searxng Ingress", URL: "https://search.example", Check: "http://searxng.searxng.svc:8080/"},
		{Name: "Photos", URL: "https://photos.example", Check: "http://lychee.lychee.svc:91/"},
	}

	got := catalog.Merge(file, discovered)

	if len(got) != 2 {
		t.Fatalf("got %d resources, want 2", len(got))
	}

	// The hand-written entry wins outright, including losing the discovered
	// check — an override is an override, not a partial one.
	if got[0].Name != "Search" {
		t.Errorf("got[0].Name = %q, want the file's name", got[0].Name)
	}

	if got[1].Name != "Photos" {
		t.Errorf("got[1].Name = %q", got[1].Name)
	}
}

func TestMergeKeepsFileOrderFirst(t *testing.T) {
	got := catalog.Merge(
		[]catalog.Resource{{Name: "B", URL: "https://b.example"}},
		[]catalog.Resource{{Name: "A", URL: "https://a.example"}},
	)

	if got[0].Name != "B" {
		t.Errorf("written-down resources should come first, got %q", got[0].Name)
	}
}
