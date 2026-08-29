// Package config loads and validates process configuration from the
// environment.
//
// Every process setting comes from an environment variable prefixed
// SCRYGLASS_. There is deliberately no config file and no flag equivalent for
// these: one source of truth means no precedence rules to reason about.
//
// The catalog is the exception, and the distinction is deliberate — process
// settings are scalars an orchestrator supplies, while the catalog is a list
// somebody edits by hand. Environment variables are bad at lists.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Prefix is prepended to every environment variable name.
const Prefix = "SCRYGLASS_"

// Config is the validated process configuration.
type Config struct {
	// Addr is the listen address. Default ":8080".
	Addr string

	// CatalogPath is the file the routing table is read from. Required.
	CatalogPath string

	// BaseURL is the externally visible root, with no trailing slash.
	// Required, because the OpenSearch descriptor has to name an absolute
	// URL and the process cannot discover what it is reachable as.
	BaseURL string

	// Title is what the page calls itself.
	Title string
}

// Load reads configuration, or reports every problem it found at once.
//
// Every problem, not the first: a deployment that fails on one missing
// variable, gets fixed, then fails on the next is three restarts to learn
// what one message could have said.
func Load() (*Config, error) {
	c := &Config{
		Addr:        envOr("ADDR", ":8080"),
		CatalogPath: os.Getenv(Prefix + "CATALOG"),
		BaseURL:     strings.TrimRight(os.Getenv(Prefix+"BASE_URL"), "/"),
		Title:       envOr("TITLE", "Scryglass"),
	}

	var problems []string

	if c.CatalogPath == "" {
		problems = append(problems, Prefix+"CATALOG is required (path to the catalog file)")
	}

	switch c.BaseURL {
	case "":
		problems = append(problems, Prefix+"BASE_URL is required (the externally visible root, e.g. https://home.example.com)")
	default:
		u, err := url.Parse(c.BaseURL)
		if err != nil || !u.IsAbs() || u.Host == "" {
			problems = append(problems, Prefix+"BASE_URL must be an absolute URL with a host")
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}

	return c, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(Prefix + name); v != "" {
		return v
	}

	return fallback
}
