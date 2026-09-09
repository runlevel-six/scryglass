# Put it behind an authenticating proxy

Scryglass has no authentication of its own, and should not gain any. It is an
omnibox over your own services and a list of everything you run — reachable
without a gate, it hands anyone a search box into your infrastructure.

This is written against Traefik and Authelia. The shape is the same for
ingress-nginx, oauth2-proxy, Authentik, or anything else that can ask an
external service whether a request is allowed — only the endpoint path and one
annotation change, and both are called out below.

## Annotate the Ingress

```yaml
metadata:
  annotations:
    nginx.ingress.kubernetes.io/auth-url: >-
      http://authelia.authelia.svc.cluster.local:9091/api/authz/forward-auth
    nginx.ingress.kubernetes.io/auth-response-headers: >-
      Remote-User,Remote-Name,Remote-Groups,Remote-Email
```

Yes, those are `nginx.ingress.kubernetes.io` annotations on a Traefik
deployment. Traefik v3.7's ingress-nginx compatibility provider reads them
directly, which is what makes this portable — see
`deploy/authelia/forward-auth.md`.

Two details are easy to get wrong and fail in ways that do not look like
configuration errors.

**The endpoint path has to match the proxy, not the annotation.** Authelia
exposes one per proxy and they are different protocols:
`/api/authz/forward-auth` reads the `X-Forwarded-*` headers Traefik and Caddy
send and answers with a redirect; `/api/authz/auth-request` reads the
`X-Original-URL` header ingress-nginx sends and answers 200 or 401. On
ingress-nginx, use `auth-request` and add:

```yaml
    nginx.ingress.kubernetes.io/auth-signin: >-
      https://auth.example.com?rm=$request_method
```

Mismatch fails closed. Traefik against the nginx endpoint answers a bare **400**
to every request, and the only explanation is in Authelia's log. Note that
Traefik's *nginx compatibility provider* still needs `forward-auth`: the
annotations are nginx-flavored but Traefik is the one asking.

**`auth-signin` belongs to ingress-nginx only.** On the forward-auth endpoint
Authelia builds the redirect itself and supplies both `rd=` and `rm=`, taking
the method from `X-Forwarded-Method`. A GET gets a 302, a POST gets a 303 with
`rm=POST` — so the classic failure this annotation guarded against, a POST
returning as a GET after sign-in with the action silently lost, does not arise.
On ingress-nginx it is required, and `rm=$request_method` is the part that
matters, not `rd=`.

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
