# Troubleshooting

Most of what goes wrong here fails *silently* — a page that renders correctly
while doing nothing. This page is organised by symptom.

## A tile will not go green

**Read the error before theorising. The three you will see mean different
things:**

| Error | Cause |
|---|---|
| *timeout / context deadline exceeded* | Something is dropping the packets. Almost always a NetworkPolicy: probes bypass the ingress, so a policy admitting only the ingress controller denies them. |
| *connection refused* | Nothing is listening on that port. Usually a resolved port that is wrong — check the Service. |
| *EOF* | The connection was accepted and closed without an answer. The server is there and declined to speak. Very often it wants TLS on a port assumed to be plaintext. |

Only state **transitions** are logged, so something that has been down since
startup logged once and then went quiet:

```sh
kubectl -n scryglass logs deploy/scryglass | grep "state changed"
```

To read current state instead — including each tile's error, which lives in its
tooltip — go through the API server and bypass the ingress and its
authentication entirely:

```sh
kubectl get --raw \
  "/api/v1/namespaces/scryglass/services/scryglass:http/proxy/tiles"
```

For a timeout, widen the NetworkPolicy or set `check: off`. For an EOF, try
forcing the scheme:

```yaml
scryglass.local/probe: "https://service.namespace.svc:32400/identity"
```

## Every status bar is empty

Expected after a restart. The bar fills from the right over
`SCRYGLASS_BAR_WINDOW` — thirty minutes by default. Segments with no samples
are drawn faintly rather than green on purpose: an empty history is not a
healthy history, and a bar that came up fully green would be claiming a half
hour it never observed.

## Tiles show but never update without a refresh

The event stream is not reaching the browser, because something in front of it
is buffering the response.

Scryglass sends `X-Accel-Buffering: no`, which is an **nginx-specific** hint.
ingress-nginx honors it; most other proxies ignore it entirely, so it is a hint
and not a guarantee.

On Traefik it is ignored — and the stream works anyway, for a different reason:
Traefik does not buffer responses at all unless a Buffering middleware is
configured, and the ingress-nginx compatibility provider only builds one when
`proxyBuffering` is enabled. So the thing to check is not the header but that
setting. Turning response buffering on breaks this stream, and
`X-Accel-Buffering` will not save it.

If the page is behind an authenticating proxy and this started after a while,
it is a session expiry: the client detects a redirected fetch and reloads,
which is the only thing that can actually sign you back in.

## Signing out, then visiting, fails to load the sign-in page

The sign-in host is wrong, and a typo there does not look like one: it produces
a browser-level connection failure naming a hostname close enough to the real
one to read as "the identity provider is down".

Where that host comes from depends on the proxy. On ingress-nginx it is the
`auth-signin` annotation, and nginx redirects to whatever it says. On Traefik
there is no such annotation — Authelia builds the redirect itself from
`session.cookies[].authelia_url` in its own configuration, so a wrong host is
an Authelia config error rather than an Ingress one.

The tell is the same either way: going to the identity provider **directly**
works, and only arriving via Scryglass fails. That path never touches the
redirect.

A bare **400** instead is a different fault — the `auth-url` endpoint does not
match the proxy. See `deploy/authelia/forward-auth.md`.

```sh
kubectl -n scryglass get ingress scryglass \
  -o jsonpath='{.metadata.annotations}' | tr ',' '\n' | grep auth
```

## The tile wall is empty, or only shows what is in the catalog

Discovery failed and kept the previous list — which after a restart is only the
catalog file. A failed pass logs a warning rather than emptying the wall:

```sh
kubectl -n scryglass logs deploy/scryglass | grep -i discovery
```

A 403 means the ServiceAccount is missing `list` on `ingresses` or `services`.
Both are needed: Services are listed to resolve named backend ports.

## The bookmarklet does nothing when dragged

**Firefox refuses to create a bookmark from a dragged `javascript:` link**, and
does so silently. The link itself is fine — clicking it works.

Right-click your bookmarks toolbar, choose *Add Bookmark*, and paste the
address from the copyable field on the bookmarks page.

## Adding a bookmark leaves a bare "Saved." page

You reached the add form with `?popup=1` outside a popup window.
`window.close()` does nothing to a tab the script did not open. Open
`/bookmarks/new` without that parameter and saving returns you to the list.

## A setting mysteriously holds another setting's value

A kustomize JSON patch addressing `env` by list index. It is silently wrong the
first time a variable is inserted above the one being patched — it overwrites
its neighbour.

Use a strategic merge patch instead; `env` merges on the `name` key:

```yaml
patch: |
  apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: scryglass
  spec:
    template:
      spec:
        containers:
          - name: scryglass
            env:
              - name: SCRYGLASS_BASE_URL
                value: https://home.example.com
```
