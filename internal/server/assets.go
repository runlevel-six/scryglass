package server

import (
	"embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/runlevel-six/scryglass/internal/version"
)

//go:embed assets
var assetsFS embed.FS

// asset is one embedded file, read once at startup.
type asset struct {
	body        []byte
	contentType string
}

// assets is every servable file, keyed by name.
//
// An allowlist built at startup rather than a path taken from the request.
// Reading a file named by a request is how directory traversal happens, and
// path.Base is a defense that has to be remembered — a map lookup cannot be
// forgotten, and a name that is not in it is simply not a file here.
var assets = mustLoadAssets()

func mustLoadAssets() map[string]asset {
	entries, err := assetsFS.ReadDir("assets")
	if err != nil {
		panic(fmt.Sprintf("read embedded assets: %v", err))
	}

	out := make(map[string]asset, len(entries))

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		body, err := assetsFS.ReadFile("assets/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("read embedded asset %s: %v", e.Name(), err))
		}

		out[e.Name()] = asset{body: body, contentType: contentType(e.Name())}
	}

	return out
}

// assetBase is the URL prefix every asset is served under, stamped with the
// build version.
//
// Versioned because favicons cache brutally — browsers keep them well past any
// max-age, and across sessions. A path that changes with the release is the
// only reliable way to make a new icon appear without asking somebody to clear
// their browser cache.
func assetBase() string {
	return "/assets/" + version.Version + "/"
}

// handleAsset serves an embedded asset.
//
// Anything under the versioned prefix is immutable by construction: the
// version is in the path, so the bytes behind a given URL never change.
func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	a, ok := assets[r.PathValue("file")]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	writeAll(w, a.body, s.log)
}

// handleFavicon serves /favicon.ico, which browsers request whether or not a
// page links one — and always at that exact path, so it cannot be versioned.
//
// A day of caching rather than a year, since this is the one asset URL that
// has to stay stable across releases.
func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	a, ok := assets["favicon.ico"]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	writeAll(w, a.body, s.log)
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}
