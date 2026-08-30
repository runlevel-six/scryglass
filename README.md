<p align="center">
  <img src="internal/server/assets/mark.png" alt="" width="180">
</p>

<h1 align="center">Scryglass</h1>

<p align="center"><em>Search your own things first, then the web.</em></p>

---

A homepage for a Kubernetes cluster you run yourself. One page holding three
things that are usually three applications:

- **An omnibox** that routes what you type. A name jumps straight to that
  service, a bang searches somewhere specific, and anything else falls through
  to your search engine.
- **A tile wall** discovered from your Ingress objects, health-checked, and
  updated live.
- **Bookmarks**, with tags and full-text search, and a bookmarklet to add them.

## The idea

A dashboard tile, an uptime monitor and a bookmark are the same record written
down three times: a URL, a name, and — sometimes — a health check. Scryglass
keeps one list. A bookmark is a resource with no check; a tile is one with a
heartbeat.

The tile half maintains itself. Every service already declares its canonical
address in an Ingress, so that is where the wall comes from — annotate an
Ingress to change a name or a probe, and nothing needs redeploying. A
hand-maintained tile list goes stale, and a stale dashboard stops being read.

## Quick start

Scryglass needs an externally visible URL and a catalog file. Bookmarks and
cluster discovery are both optional; without them it is a working omnibox.

```sh
docker run --rm -p 8080:8080 \
  -e SCRYGLASS_BASE_URL=http://localhost:8080 \
  -e SCRYGLASS_CATALOG=/catalog.yaml \
  -v "$PWD/catalog.example.yaml:/catalog.yaml:ro" \
  ghcr.io/runlevel-six/scryglass:latest
```

Then open it, and register it as your browser's search engine — the page
publishes an OpenSearch descriptor, so bangs work from the address bar in every
tab rather than only on the homepage.

On Kubernetes, `deploy/scryglass/overlays/example` is a worked overlay. Copy it
to `overlays/local`, put your own hosts in it, and apply. Discovery needs a
read-only ServiceAccount with `list` on ingresses and services — nothing else,
and no write verbs anywhere.

## Configuration

| Variable | Default | |
|---|---|---|
| `SCRYGLASS_BASE_URL` | — | Required. The externally visible root. |
| `SCRYGLASS_CATALOG` | — | Required. Path to the catalog file. |
| `SCRYGLASS_ADDR` | `:8080` | Listen address. |
| `SCRYGLASS_TITLE` | `Scryglass` | What the page calls itself. |
| `SCRYGLASS_DATABASE` | — | Bookmark database. Unset disables bookmarks. |
| `SCRYGLASS_LOGOUT_URL` | — | Your identity provider's sign-out address. Unset hides the control. |
| `SCRYGLASS_DISCOVERY` | `false` | Read Ingress objects to build the wall. |
| `SCRYGLASS_ANNOTATION_PREFIX` | `scryglass.local` | Annotation namespace for per-Ingress overrides. |
| `SCRYGLASS_DISCOVERY_INTERVAL` | `60s` | How often the cluster is re-read. |
| `SCRYGLASS_PROBE_INTERVAL` | `30s` | How often each resource is checked. |
| `SCRYGLASS_PROBE_TIMEOUT` | `5s` | How long one check may take. |
| `SCRYGLASS_BAR_WINDOW` | `30m` | How far back a tile's status bar reaches. |

`catalog.example.yaml` documents the catalog format, including how a
hand-written entry corrects a discovered one.

## A note on what it does not do

There is no alerting. The question this answers is "what is red when I look at
the page", and stopping there is what keeps it one small binary instead of a
second application to operate.
