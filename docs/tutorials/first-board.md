# Your first board

By the end of this you will have Scryglass running locally with a search box
that works, then running on a cluster with a tile wall it built itself.

It takes about fifteen minutes. You need Docker for the first half and a
Kubernetes cluster with an Ingress controller for the second.

## Run it locally

Scryglass needs two things: a catalog file, and the address it will be reached
at. Nothing else is required — bookmarks and cluster discovery are both
optional.

Copy the example catalog out of the repository, or write this one:

```yaml
# catalog.yaml
search:
  name: Web
  url: "https://duckduckgo.com/?q={query}"

resources:
  - name: Docs
    url: https://kubernetes.io/docs/
    aliases: [k8s]
```

Then:

```sh
docker run --rm -p 8080:8080 \
  -e SCRYGLASS_BASE_URL=http://localhost:8080 \
  -e SCRYGLASS_CATALOG=/catalog.yaml \
  -v "$PWD/catalog.yaml:/catalog.yaml:ro" \
  ghcr.io/runlevel-six/scryglass:latest
```

Open <http://localhost:8080>. The cursor is already in the box.

Try three things, because they are three different behaviours:

- Type `docs` and press Enter. You go straight to the Kubernetes docs — that
  is a resource match.
- Type `k8s`. Same place, via an alias.
- Type `ingress controller`. Nothing matches, so it falls through to your
  search engine.

That is the whole omnibox. Everything else is refinement.

## Put it on a cluster

Create a namespace by hand — deliberately not part of the kustomization, so
that `kubectl delete -k` cannot take the namespace and every volume in it:

```sh
kubectl create namespace scryglass
```

Copy the worked overlay and edit it:

```sh
cp -r deploy/scryglass/overlays/example deploy/scryglass/overlays/local
```

In `overlays/local/` you need to change three things:

- `catalog.yaml` — your search engine and any bookmarks you want in the box.
- `kustomization.yaml` — the Ingress host, and `SCRYGLASS_BASE_URL` to match.
- The `storageClassName` patch, if you want bookmarks. It must be a block-mode
  class, not a network filesystem: bookmarks live in SQLite, whose locking is
  unreliable over NFS-alikes.

Then apply:

```sh
kubectl apply -k deploy/scryglass/overlays/local
kubectl -n scryglass rollout status deploy/scryglass
```

## Watch the wall build itself

The overlay enables discovery, so within a minute the page grows a tile for
every Ingress in your cluster — name, address and a health check, none of which
you typed.

The checks do not probe the public address. They probe the Service behind each
Ingress, which is the only way to learn anything about an application sitting
behind an authenticating proxy: a probe of the public host gets the login
page's redirect, which proves the proxy is alive and says nothing about the
application. See [Design](../explanation/design.md).

Two things will look wrong at first, and both are correct:

**Every status bar is empty.** It fills in from the right as history
accumulates, over thirty minutes by default. A bar that came up fully green
after a restart would be claiming a half hour it never observed.

**Some names are ugly.** They are derived from Ingress object names, so an
Ingress called `searxng-ingress` gives you a tile called "Searxng Ingress". One
annotation fixes it, with no redeploy — see
[Change what a discovered tile looks like](../how-to/customize-tiles.md).

## Next

- [Set it up in your browser](../how-to/browser-setup.md) is what turns this
  from a page you visit into the one you type into. It is the step that decides
  whether you keep using it.
- [Put it behind an authenticating proxy](../how-to/behind-a-proxy.md), before
  it is reachable from anywhere you would not want a search box into your own
  services.
