# Change what a discovered tile looks like

Discovery derives a tile from an Ingress: the name from the object's name, the
address from the rule's host, and a health check from the backing Service. It
is right often enough to be worth doing and wrong often enough to need
overriding.

Everything here is an annotation on the Ingress, read within one discovery
interval — sixty seconds by default. **Nothing needs redeploying.**

```sh
kubectl -n media annotate ingress plex scryglass.local/name=Plex
```

The full set is in the [annotation reference](../reference/annotations.md).
This page is the four you will actually reach for.

## Fix an ugly name

Names come from the Ingress object, so an Ingress called `searxng-ingress`
becomes "Searxng Ingress" and one called `summitridge` becomes "Summitridge".

```yaml
scryglass.local/name: "Search"
```

## Group tiles

Ungrouped tiles collect at the end of the wall under a heading of their own.
Grouping a few of them is usually a bigger improvement than any amount of
reordering:

```yaml
scryglass.local/group: "infra"
```

Groups appear in the order their first member appears, and that order comes
from the catalog file. There is no separate ordering mechanism — to move a
tile, name it in the catalog in the position you want. Two lines is enough;
everything else is still inherited from discovery.

## Fix a check that will not go green

The probe URL is derived from the Ingress backend, and the scheme is guessed
from the port number. That guess is sometimes wrong in a way no smarter guess
would fix — a media server that also speaks TLS on its plaintext port simply
closes the connection, which is indistinguishable from being down.

A path, appended to the derived address:

```yaml
scryglass.local/probe: "/api/health"
```

Or a complete URL, replacing it entirely — which is how you override a scheme,
a port, or the host:

```yaml
scryglass.local/probe: "https://plex.media.svc:32400/identity"
```

## Stop a tile alarming, or showing at all

For something deliberately turned off, so it dims instead of going red:

```yaml
scryglass.local/retired: "true"
```

Retired resources are never probed at all, rather than probed and ignored. A
wall of red for things you switched off yourself is how a dashboard stops being
read.

To remove it from the wall entirely:

```yaml
scryglass.local/hide: "true"
```
