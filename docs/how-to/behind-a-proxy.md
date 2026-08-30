# Put it behind an authenticating proxy

Scryglass has no authentication of its own, and should not gain any. It is an
omnibox over your own services and a list of everything you run — reachable
without a gate, it hands anyone a search box into your infrastructure.

This is written against ingress-nginx and Authelia. The shape is the same for
oauth2-proxy, Authentik, or anything else that speaks `auth_request`.

## Annotate the Ingress

```yaml
metadata:
  annotations:
    nginx.ingress.kubernetes.io/auth-url: >-
      http://authelia.authelia.svc.cluster.local:9091/api/authz/auth-request
    nginx.ingress.kubernetes.io/auth-signin: >-
      https://auth.example.com?rm=$request_method
    nginx.ingress.kubernetes.io/auth-response-headers: >-
      Remote-User,Remote-Name,Remote-Groups,Remote-Email
```

Three details in there are easy to get wrong and fail in ways that do not look
like configuration errors.

**`auth-request`, not `forward-auth`.** Authelia exposes both and they are
different protocols rather than aliases. `forward-auth` answers with a redirect
and is what Traefik and Caddy want; `auth-request` answers 200 or 401 and lets
nginx do the redirecting, which is what the `auth-url` annotation drives.

**`rm=$request_method`, not `rd=$target_url`.** ingress-nginx appends the
return destination itself; what has to be passed explicitly is the original
method. Without it, a POST to a protected page comes back as a GET after
sign-in — the login succeeds and the action you took vanishes.

**The sign-in host has to be right, and a typo does not look like one.** nginx
redirects to whatever the annotation says. A host that does not resolve
produces a browser-level connection failure naming a hostname close enough to
the real one to read as "the identity provider is down". If signing in directly
works but arriving via Scryglass does not, read that annotation character by
character.

## Add the sign-out control

Set `SCRYGLASS_LOGOUT_URL` to your provider's sign-out address —
`/logout` for Authelia, `/oauth2/sign_out` for oauth2-proxy. There is no
default, because a wrong guess is a button that appears to work and does
nothing. Unset, the control is simply absent.

The page also shows who is signed in, from the `Remote-Name` header the
annotations above pass through.

**That name is cosmetic and must stay that way.** The Service behind the proxy
is reachable by anything else in the cluster, so a header is a claim rather
than a fact. Scryglass has no authorization to do and nothing here should
start.

## What not to gate

Anything without a browser cannot pass this gate, and breaks *silently* when
you try. Media player apps, note-sync clients, anything speaking an API with
its own credentials — none of them can follow a redirect to a login form. Leave
those ungated and rely on their own authentication.

The rule to re-check every time you gate something new: **if it is not a
browser, it cannot pass.**
