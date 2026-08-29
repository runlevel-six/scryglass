// Package kube reads Ingress objects from the Kubernetes API.
//
// It speaks to the API directly over HTTP rather than through client-go, and
// that is a deliberate trade rather than an oversight.
//
// What client-go buys is watch semantics: informer caches, resourceVersion
// bookkeeping, 410 Gone recovery, resync. None of that is needed to draw a
// tile wall. This lists Ingresses on an interval and rebuilds; the worst case
// is that a newly deployed application takes one interval to appear, which for
// a dashboard nobody is staring at is not a defect. In exchange the binary
// keeps a dependency tree small enough to read.
//
// If this ever grows into something that must react to a change rather than
// notice it, that is the moment to take the dependency — not before.
package kube

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// The paths a projected ServiceAccount volume is mounted at. These are
	// fixed by Kubernetes, not by us.
	tokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token" //nolint:gosec // G101: a path, not a credential
	caPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

	defaultHost = "https://kubernetes.default.svc"
)

// Client is a read-only Kubernetes API client.
type Client struct {
	host string
	http *http.Client

	// tokenPath is read on every request rather than once at startup.
	//
	// Projected ServiceAccount tokens expire and are rewritten in place by the
	// kubelet, typically hourly. A client that caches the token works
	// perfectly until the first rotation and then returns 401 forever, which
	// looks like a permissions problem and is not one.
	tokenPath string
}

// InCluster builds a Client from the ServiceAccount mounted into this pod.
func InCluster() (*Client, error) {
	ca, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read cluster CA (is this running in a pod?): %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("cluster CA bundle contains no usable certificates")
	}

	host := defaultHost
	if h := os.Getenv("KUBERNETES_SERVICE_HOST"); h != "" {
		port := os.Getenv("KUBERNETES_SERVICE_PORT")
		if port == "" {
			port = "443"
		}

		host = "https://" + net.JoinHostPort(h, port)
	}

	return &Client{
		host:      host,
		tokenPath: tokenPath,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			},
		},
	}, nil
}

// get performs an authenticated GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	token, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return fmt.Errorf("read service account token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+path, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 403 is the interesting one and the message says so, because the
		// cause is almost always a missing ClusterRole rule rather than a bad
		// token, and the two look identical from here.
		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("GET %s: 403 Forbidden — the ServiceAccount needs list on ingresses (networking.k8s.io)", path)
		}

		return fmt.Errorf("GET %s: unexpected status %s", path, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}

	return nil
}
