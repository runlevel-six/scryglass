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
	"time"
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

	// Discovery turns on reading Ingress objects from the cluster. Off by
	// default so the binary runs anywhere: outside Kubernetes it should
	// degrade to a working omnibox over the file, not fail to start.
	Discovery bool

	// AnnotationPrefix is the annotation namespace discovery reads, without a
	// trailing slash. Configurable because it is written into other people's
	// manifests, and baking one project's domain in would be rude.
	AnnotationPrefix string

	// DiscoveryInterval is how often the cluster is re-read.
	DiscoveryInterval time.Duration

	// ProbeInterval is how often each resource is checked.
	ProbeInterval time.Duration

	// ProbeTimeout is how long a single check may take.
	ProbeTimeout time.Duration

	// BarWindow is how far back a tile's status bar reaches.
	//
	// Thirty minutes rather than an hour by default, because the bar fills at
	// one segment per bucket and until it fills it is grey: a longer window
	// means a wall that looks broken for longer after every deploy. Note the
	// prober keeps a bounded number of samples, so a very short probe interval
	// paired with a long window here will never fill.
	BarWindow time.Duration
}

// SelfHost is the host part of BaseURL.
//
// Discovery skips it: Scryglass has an Ingress like everything else, and a
// tile linking to the page you are already looking at is noise.
func (c *Config) SelfHost() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return ""
	}

	return u.Host
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

		Discovery:        isTrue(os.Getenv(Prefix + "DISCOVERY")),
		AnnotationPrefix: strings.TrimSuffix(envOr("ANNOTATION_PREFIX", "scryglass.local"), "/"),
	}

	var durProblems []string

	c.DiscoveryInterval, durProblems = duration("DISCOVERY_INTERVAL", 60*time.Second, durProblems)
	c.ProbeInterval, durProblems = duration("PROBE_INTERVAL", 30*time.Second, durProblems)
	c.ProbeTimeout, durProblems = duration("PROBE_TIMEOUT", 5*time.Second, durProblems)
	c.BarWindow, durProblems = duration("BAR_WINDOW", 30*time.Minute, durProblems)

	problems := durProblems

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

// duration parses a duration variable, collecting rather than returning the
// problem so that Load can report every complaint at once.
func duration(name string, fallback time.Duration, problems []string) (time.Duration, []string) {
	raw := os.Getenv(Prefix + name)
	if raw == "" {
		return fallback, problems
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback, append(problems, Prefix+name+" is not a duration (try 30s, 2m): "+raw)
	}

	if d <= 0 {
		return fallback, append(problems, Prefix+name+" must be positive")
	}

	return d, problems
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1":
		return true
	default:
		return false
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(Prefix + name); v != "" {
		return v
	}

	return fallback
}
