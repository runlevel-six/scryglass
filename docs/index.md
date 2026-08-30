# Documentation

Scryglass is a homepage for a Kubernetes cluster you run yourself: an omnibox
that routes what you type, a tile wall discovered from your Ingress objects,
and bookmarks. Start with the [README](../README.md) for what it is; the pages
below go deeper.

## Tutorials — learning by doing

- [Your first board](tutorials/first-board.md) — run it locally, then put it on a cluster and watch the wall build itself.

## How-to guides — a specific goal

- [Put it behind an authenticating proxy](how-to/behind-a-proxy.md)
- [Change what a discovered tile looks like](how-to/customize-tiles.md)
- [Set it up in your browser](how-to/browser-setup.md) — new tab, search engine, bookmarklet.

## Reference — look it up

- [Configuration](reference/configuration.md) — every environment variable.
- [The catalog file](reference/catalog.md) — format, and how it corrects discovery.
- [Ingress annotations](reference/annotations.md) — per-service overrides.
- [The omnibox](reference/omnibox.md) — how a query is routed, and in what order.

## Explanation — why it works this way

- [Design](explanation/design.md) — one resource model, discovery over a kept list, and why there is no alerting.

## Something's wrong

- [Troubleshooting](explanation/troubleshooting.md) — tiles that will not go green, redirect loops, and a bookmarklet that does nothing.
