package kube

import (
	"testing"
)

func ing(ns, name, host, svc string, portNum int, portName string, ann map[string]string) ingress {
	var in ingress

	in.Metadata.Name = name
	in.Metadata.Namespace = ns
	in.Metadata.Annotations = ann

	rule := struct {
		Host string `json:"host"`
		HTTP struct {
			Paths []struct {
				Backend struct {
					Service struct {
						Name string `json:"name"`
						Port struct {
							Name   string `json:"name"`
							Number int    `json:"number"`
						} `json:"port"`
					} `json:"service"`
				} `json:"backend"`
			} `json:"paths"`
		} `json:"http"`
	}{Host: host}

	var path struct {
		Backend struct {
			Service struct {
				Name string `json:"name"`
				Port struct {
					Name   string `json:"name"`
					Number int    `json:"number"`
				} `json:"port"`
			} `json:"service"`
		} `json:"backend"`
	}

	path.Backend.Service.Name = svc
	path.Backend.Service.Port.Number = portNum
	path.Backend.Service.Port.Name = portName

	rule.HTTP.Paths = append(rule.HTTP.Paths, path)
	in.Spec.Rules = append(in.Spec.Rules, rule)

	return in
}

const prefix = "scryglass.local"

func TestDiscoveryDefaults(t *testing.T) {
	got := resourcesFrom(
		[]ingress{ing("uptime-kuma", "uptime-kuma", "uptime.example.com", "uptime-kuma", 3001, "", nil)},
		Options{AnnotationPrefix: prefix},
	)

	if len(got) != 1 {
		t.Fatalf("got %d resources, want 1", len(got))
	}

	r := got[0]

	if r.Name != "Uptime Kuma" {
		t.Errorf("Name = %q, want %q", r.Name, "Uptime Kuma")
	}

	if r.URL != "https://uptime.example.com" {
		t.Errorf("URL = %q", r.URL)
	}

	// No default group: one application per namespace would produce a
	// section per tile, which is not grouping.
	if r.Group != "" {
		t.Errorf("Group = %q, want empty by default", r.Group)
	}

	// The check must target the Service, never the public host. Probing the
	// public host of a gated application returns the login page's redirect and
	// says nothing about whether the application is alive.
	want := "http://uptime-kuma.uptime-kuma.svc:3001/"
	if r.Check != want {
		t.Errorf("Check = %q, want %q", r.Check, want)
	}
}

func TestAnnotationsOverrideEverything(t *testing.T) {
	got := resourcesFrom(
		[]ingress{ing("lychee", "lychee", "photos.example.com", "lychee", 91, "", map[string]string{
			prefix + "/name":    "Photos",
			prefix + "/group":   "media",
			prefix + "/icon":    "camera",
			prefix + "/aliases": "gallery, pics",
			prefix + "/probe":   "/api/health",
			prefix + "/retired": "true",
		})},
		Options{AnnotationPrefix: prefix},
	)

	r := got[0]

	if r.Name != "Photos" || r.Group != "media" || r.Icon != "camera" {
		t.Errorf("annotations not applied: %+v", r)
	}

	if len(r.Aliases) != 2 || r.Aliases[0] != "gallery" || r.Aliases[1] != "pics" {
		t.Errorf("Aliases = %v", r.Aliases)
	}

	if !r.Retired {
		t.Error("Retired should be true")
	}

	if want := "http://lychee.lychee.svc:91/api/health"; r.Check != want {
		t.Errorf("Check = %q, want %q", r.Check, want)
	}
}

func TestHideAndSelfAreSkipped(t *testing.T) {
	items := []ingress{
		ing("a", "hidden", "hidden.example.com", "a", 80, "", map[string]string{prefix + "/hide": "true"}),
		ing("b", "self", "home.example.com", "b", 80, "", nil),
		ing("c", "kept", "kept.example.com", "c", 80, "", nil),
	}

	got := resourcesFrom(items, Options{AnnotationPrefix: prefix, SelfHost: "home.example.com"})

	if len(got) != 1 || got[0].URL != "https://kept.example.com" {
		t.Fatalf("got %+v, want only the kept ingress", got)
	}
}

// Not every backend speaks plaintext. The port is the only hint an Ingress
// gives, so a named port containing "https" and the conventional TLS numbers
// both have to select the right scheme.
func TestHTTPSBackends(t *testing.T) {
	cases := []struct {
		name     string
		portNum  int
		portName string
		want     string
	}{
		{"named https port resolves via the Service", 0, "https-dashboard", "https://s.ns.svc:8443/"},
		{"conventional tls number", 10443, "", "https://s.ns.svc:10443/"},
		{"plain http", 8080, "", "http://s.ns.svc:8080/"},
		{"named http port resolves to whatever the Service says", 0, "http", "http://s.ns.svc:9091/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The index deliberately disagrees with the conventional
			// numbers: Authelia really does call port 9091 "http", and
			// assuming 80 would probe a closed port and report a healthy
			// application as down.
			ports := portIndex{
				"ns/s/https-dashboard": 8443,
				"ns/s/http":            9091,
			}

			got := resourcesFromWithPorts(
				[]ingress{ing("ns", "x", "x.example.com", "s", tc.portNum, tc.portName, nil)},
				ports, Options{AnnotationPrefix: prefix},
			)

			if got[0].Check != tc.want {
				t.Errorf("Check = %q, want %q", got[0].Check, tc.want)
			}
		})
	}
}

// An Ingress with no usable backend still produces a resource — it is a link
// that works — but with no check, which makes it a bookmark rather than a tile
// that can go red.
func TestNoBackendMeansNoCheck(t *testing.T) {
	in := ing("ns", "x", "x.example.com", "", 0, "", nil)

	got := resourcesFrom([]ingress{in}, Options{AnnotationPrefix: prefix})
	if len(got) != 1 {
		t.Fatalf("got %d resources, want 1", len(got))
	}

	if got[0].Check != "" {
		t.Errorf("Check = %q, want empty", got[0].Check)
	}
}

// A named port that resolves to nothing yields no check at all. A guess would
// probe the wrong port and report a healthy application as down, which is
// worse than an unmonitored tile.
func TestUnresolvableNamedPortMeansNoCheck(t *testing.T) {
	got := resourcesFromWithPorts(
		[]ingress{ing("ns", "x", "x.example.com", "s", 0, "grpc", nil)},
		portIndex{}, Options{AnnotationPrefix: prefix},
	)

	if len(got) != 1 {
		t.Fatalf("got %d resources, want 1", len(got))
	}

	if got[0].Check != "" {
		t.Errorf("Check = %q, want empty", got[0].Check)
	}

	if got[0].URL == "" {
		t.Error("the link should still work even with no check")
	}
}
