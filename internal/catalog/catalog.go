// Package catalog holds the things the omnibox can send you to.
//
// There is one entity here, not three. A Resource is a URL with a name and,
// later, an optional health check: a bookmark is a Resource with no check, an
// app tile is a Resource with one. Keeping them the same record is the reason
// this project exists — the dashboard, the monitor list and the bookmark file
// it replaces were the same list written down three times.
//
// In M1 the catalog is read from a file. In M2 the app-tile half of it is
// discovered from Ingress objects instead, and the file keeps only what the
// cluster cannot tell us. The type is shaped for that: nothing here knows
// where a Resource came from.
package catalog

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Resource is somewhere the omnibox can send you.
type Resource struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`

	// Group is the tile section this belongs to. Unused by the router; it
	// exists because M2 renders by it and the file format should not have to
	// change then.
	Group string `yaml:"group,omitempty"`

	// Aliases are additional names that match this resource. "pics" for a
	// photo gallery, "k8s" for a cluster dashboard — whatever you actually
	// type when you are not thinking about what the thing is called.
	Aliases []string `yaml:"aliases,omitempty"`

	// Icon names the tile's glyph. Empty means the tile falls back to the
	// resource's initial.
	Icon string `yaml:"icon,omitempty"`

	// Check is the URL to probe to decide whether this resource is up. Empty
	// means it is never probed, which is what makes a bookmark a bookmark.
	//
	// This is deliberately not URL. For anything running in the cluster the
	// two differ and the difference is the point: URL is the public address,
	// which for a gated host answers every unauthenticated request with a
	// redirect to a login page. Probing that proves the ingress and the
	// identity provider are alive and says nothing whatsoever about the
	// application. Check points at the backing Service instead.
	Check string `yaml:"check,omitempty"`

	// Retired dims the tile instead of alarming about it. For things that are
	// deliberately not running rather than unexpectedly down.
	Retired bool `yaml:"retired,omitempty"`
}

// Bang is a prefix that routes a query somewhere specific.
//
// The URL is a template containing {query}, which is replaced with the
// percent-encoded remainder of the input. Encoding assumes the placeholder
// sits in a query string, which is true of every search endpoint this is
// pointed at; a template that puts it in a path would need different escaping.
type Bang struct {
	Key  string `yaml:"key"`
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Search is where a query goes when nothing else claims it.
type Search struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Catalog is the whole routing table.
type Catalog struct {
	Search    Search     `yaml:"search"`
	Bangs     []Bang     `yaml:"bangs"`
	Resources []Resource `yaml:"resources"`
}

// Load reads and validates a catalog file.
//
// Validation is strict and happens once, at startup. A router that starts with
// a broken template and discovers it when somebody types a query has moved a
// startup failure into a user-facing one.
func Load(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied path, by design
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}

	var c Catalog

	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true) // a typo'd key is a mistake, not something to ignore

	if err = dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", path, err)
	}

	if err = c.Validate(); err != nil {
		return nil, fmt.Errorf("catalog %s: %w", path, err)
	}

	return &c, nil
}

// Placeholder is what a Bang or Search URL template substitutes.
const Placeholder = "{query}"

// Validate reports the first problem that would make routing misbehave.
func (c *Catalog) Validate() error {
	if c.Search.URL == "" {
		return fmt.Errorf("search.url is required: with nothing to fall back to, any query that matches no resource or bang has nowhere to go")
	}

	if !strings.Contains(c.Search.URL, Placeholder) {
		return fmt.Errorf("search.url must contain %s", Placeholder)
	}

	seen := make(map[string]bool, len(c.Bangs))

	for i, b := range c.Bangs {
		switch {
		case b.Key == "":
			return fmt.Errorf("bangs[%d]: key is required", i)
		case strings.HasPrefix(b.Key, "!"):
			return fmt.Errorf("bangs[%d]: key %q is written without its leading exclamation mark", i, b.Key)
		case !strings.Contains(b.URL, Placeholder):
			return fmt.Errorf("bangs[%d] (%s): url must contain %s", i, b.Key, Placeholder)
		case seen[strings.ToLower(b.Key)]:
			return fmt.Errorf("bangs[%d]: duplicate key %q", i, b.Key)
		}

		seen[strings.ToLower(b.Key)] = true
	}

	for i, r := range c.Resources {
		if r.Name == "" {
			return fmt.Errorf("resources[%d]: name is required", i)
		}

		if r.URL == "" {
			return fmt.Errorf("resources[%d] (%s): url is required", i, r.Name)
		}
	}

	return nil
}
