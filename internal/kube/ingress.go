package kube

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/runlevel-six/scryglass/internal/catalog"
)

// ingressList is the subset of the API response this needs. Decoding into a
// narrow type rather than a generic map means a field that changes shape
// upstream fails at the decoder instead of somewhere further in.
type ingressList struct {
	Items []ingress `json:"items"`
}

type ingress struct {
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Rules []struct {
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
		} `json:"rules"`
	} `json:"spec"`
}

// Options control how Ingresses become Resources.
type Options struct {
	// AnnotationPrefix is the annotation namespace read for overrides, without
	// a trailing slash. Configurable because it is written into other people's
	// manifests and baking one project's domain into it would be rude.
	AnnotationPrefix string

	// SelfHost is skipped if it turns up. Scryglass has an Ingress like
	// everything else, and a tile linking to the page you are looking at is
	// noise.
	SelfHost string
}

// Discover lists Ingresses and turns them into Resources.
//
// The returned resources carry a Check pointing at the backing Service, not at
// the public host. See catalog.Resource.Check for why that distinction is the
// whole point.
func (c *Client) Discover(ctx context.Context, opt Options) ([]catalog.Resource, error) {
	var list ingressList

	if err := c.get(ctx, "/apis/networking.k8s.io/v1/ingresses?limit=500", &list); err != nil {
		return nil, err
	}

	// Services are listed for one reason: an Ingress may name its backend port
	// rather than number it, and a name cannot go in a URL. The number is not
	// guessable — Authelia's Service calls port 9091 "http", so assuming 80
	// would probe a closed port and report a healthy application as down.
	var svcs serviceList

	if err := c.get(ctx, "/api/v1/services?limit=1000", &svcs); err != nil {
		return nil, err
	}

	return resourcesFromWithPorts(list.Items, indexPorts(svcs.Items), opt), nil
}

func resourcesFrom(items []ingress, opt Options) []catalog.Resource {
	return resourcesFromWithPorts(items, portIndex{}, opt)
}

func resourcesFromWithPorts(items []ingress, ports portIndex, opt Options) []catalog.Resource {
	prefix := strings.TrimSuffix(opt.AnnotationPrefix, "/")

	var out []catalog.Resource

	for _, in := range items {
		ann := func(k string) string { return in.Metadata.Annotations[prefix+"/"+k] }

		if isTrue(ann("hide")) {
			continue
		}

		for _, rule := range in.Spec.Rules {
			if rule.Host == "" || rule.Host == opt.SelfHost {
				continue
			}

			res := catalog.Resource{
				Name: firstNonEmpty(ann("name"), titleize(in.Metadata.Name)),
				URL:  "https://" + rule.Host,
				// No default group. Namespace is the obvious candidate and a
				// bad one: a cluster running one application per namespace
				// would get seventeen sections of one tile each, which is not
				// grouping, it is a list with extra headings. Sections appear
				// when somebody asks for them.
				Group:   ann("group"),
				Icon:    ann("icon"),
				Retired: isTrue(ann("retired")),
			}

			if a := ann("aliases"); a != "" {
				res.Aliases = splitList(a)
			}

			res.Check = checkURL(in, rule.Host, in.Metadata.Namespace, ann("probe"), ports)

			out = append(out, res)

			// One resource per host. An Ingress routing several paths of the
			// same host to different backends is one thing with one address as
			// far as somebody typing a name is concerned.
			break
		}
	}

	return out
}

// checkURL builds the in-cluster URL to probe, from the Ingress backend.
func checkURL(in ingress, host, namespace, probePath string, ports portIndex) string {
	for _, rule := range in.Spec.Rules {
		if rule.Host != host {
			continue
		}

		for _, p := range rule.HTTP.Paths {
			svc := p.Backend.Service
			if svc.Name == "" {
				continue
			}

			port := svc.Port.Name
			if svc.Port.Number != 0 {
				port = strconv.Itoa(svc.Port.Number)
			}

			if port == "" {
				continue
			}

			scheme := "http"
			// Not every backend speaks plaintext, and the port is the only
			// hint the Ingress gives. A named port containing "https" or the
			// conventional TLS numbers are the cases that occur in practice;
			// anything else can be corrected with the probe annotation.
			if strings.Contains(strings.ToLower(svc.Port.Name), "https") ||
				svc.Port.Number == 443 || svc.Port.Number == 8443 || svc.Port.Number == 10443 {
				scheme = "https"
			}

			// A named port cannot go in a URL. Resolve it against the
			// Service; if that fails — the Service is gone, or the name does
			// not match any of its ports — there is no honest number to use
			// and a guess would report a healthy application as down. Better
			// no check than a wrong one.
			if svc.Port.Number == 0 {
				n, ok := ports.lookup(namespace, svc.Name, svc.Port.Name)
				if !ok {
					return ""
				}

				port = strconv.Itoa(n)
			}

			authority := net.JoinHostPort(fmt.Sprintf("%s.%s.svc", svc.Name, namespace), port)

			return scheme + "://" + authority + firstNonEmpty(probePath, "/")
		}
	}

	return ""
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1":
		return true
	default:
		return false
	}
}

func splitList(s string) []string {
	var out []string

	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}

	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}

// titleize turns an object name into something worth showing on a tile:
// "rook-ceph-dashboard" becomes "Rook Ceph Dashboard".
func titleize(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if p == "" {
			continue
		}

		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}

	return strings.Join(parts, " ")
}

// serviceList is the subset of the Services API this needs, which is only
// enough to turn a named port into a number.
type serviceList struct {
	Items []service `json:"items"`
}

type service struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Ports []struct {
			Name string `json:"name"`
			Port int    `json:"port"`
		} `json:"ports"`
	} `json:"spec"`
}

// portIndex maps namespace/service/port-name to the port number.
type portIndex map[string]int

func (p portIndex) lookup(namespace, svc, portName string) (int, bool) {
	n, ok := p[namespace+"/"+svc+"/"+portName]

	return n, ok
}

func indexPorts(items []service) portIndex {
	idx := make(portIndex)

	for _, s := range items {
		for _, port := range s.Spec.Ports {
			if port.Name != "" {
				idx[s.Metadata.Namespace+"/"+s.Metadata.Name+"/"+port.Name] = port.Port
			}
		}
	}

	return idx
}
