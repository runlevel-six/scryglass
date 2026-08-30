# Configuration

Every process setting comes from an environment variable prefixed
`SCRYGLASS_`. There is deliberately no config file and no flag equivalent: one
source of truth means no precedence rules to reason about.

The catalog is the exception, and the distinction is deliberate — process
settings are scalars an orchestrator supplies, while the catalog is a list
somebody edits by hand, and environment variables are bad at lists.

Configuration is validated once, at startup, and reports **every** problem it
finds rather than the first. A deployment that fails on one missing variable,
gets fixed, then fails on the next is three restarts to learn what one message
could have said.

## Required

| Variable | |
|---|---|
| `SCRYGLASS_BASE_URL` | The externally visible root, with scheme and host — `https://home.example.com`. Used for the OpenSearch descriptor and the bookmarklet, both of which have to name an absolute URL; the process cannot discover what it is reachable as. Also the host discovery skips, so Scryglass does not render a tile linking to the page you are looking at. |
| `SCRYGLASS_CATALOG` | Path to the catalog file. See [the catalog file](catalog.md). |

## Optional

| Variable | Default | |
|---|---|---|
| `SCRYGLASS_ADDR` | `:8080` | Listen address. |
| `SCRYGLASS_TITLE` | `Scryglass` | What the page calls itself. |
| `SCRYGLASS_LOGOUT_URL` | — | Your identity provider's sign-out address. No default: it is provider-specific, and a wrong guess is a button that appears to work and does nothing. Unset hides the control. |
| `SCRYGLASS_DATABASE` | — | Where bookmarks are stored. **Unset disables bookmarks entirely**, which is a supported way to run this rather than a degraded one — it needs no volume. |

## Discovery

| Variable | Default | |
|---|---|---|
| `SCRYGLASS_DISCOVERY` | `false` | Read Ingress objects to build the tile wall. Off by default so the binary runs anywhere: outside Kubernetes it degrades to a working omnibox rather than failing to start. |
| `SCRYGLASS_ANNOTATION_PREFIX` | `scryglass.local` | The annotation namespace read for per-Ingress overrides. Configurable because it is written into other people's manifests. |
| `SCRYGLASS_DISCOVERY_INTERVAL` | `60s` | How often the cluster is re-read. A failed pass keeps the previous list rather than emptying the wall. |

Discovery needs a ServiceAccount with `list` on `ingresses` and `services`, and
nothing else — no pods, no secrets, no write verbs. Services are listed for one
reason: an Ingress may *name* its backend port rather than number it, and the
number is not guessable.

## Probing

| Variable | Default | |
|---|---|---|
| `SCRYGLASS_PROBE_INTERVAL` | `30s` | How often each resource is checked. |
| `SCRYGLASS_PROBE_TIMEOUT` | `5s` | How long one check may take. |
| `SCRYGLASS_BAR_WINDOW` | `30m` | How far back a tile's status bar reaches. It fills at one segment per bucket and reads grey until it does, so a longer window means a wall that looks unfinished for longer after every deploy. |

The prober keeps a bounded number of samples, so a very short probe interval
paired with a long bar window will never fill.

## Endpoints

| Path | |
|---|---|
| `/` | The omnibox and tile wall. |
| `/search?q=` | Routes a query and redirects. See [the omnibox](omnibox.md). |
| `/tiles` | The tile wall as an HTML fragment, for live updates. |
| `/events` | Server-sent events; one notification per tile state change. |
| `/bookmarks` | List, search and filter. Absent without a database. |
| `/opensearch.xml` | The search engine descriptor. |
| `/healthz`, `/readyz` | Liveness and readiness. |
