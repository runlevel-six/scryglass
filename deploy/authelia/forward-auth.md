# Protecting an Ingress with Authelia

These annotations are **not applied by this kustomization**. Each protected app
owns its own Ingress, usually in its own namespace and often in another
repository, so gating one is a deliberate edit to that app's manifest.

Add to the Ingress you want to protect:

```yaml
metadata:
  annotations:
    # The path depends on your ingress controller — see below.
    nginx.ingress.kubernetes.io/auth-url: >-
      http://authelia.authelia.svc.cluster.local:9091/api/authz/forward-auth
    nginx.ingress.kubernetes.io/auth-response-headers: >-
      Remote-User,Remote-Name,Remote-Groups,Remote-Email
```

## Pick the endpoint that matches your proxy

Authelia exposes one authz endpoint per proxy, and they are different protocols
rather than aliases. Each proxy describes the original request in its own way,
and each endpoint reads only its own dialect:

| Endpoint | For | Reads | Answers |
|---|---|---|---|
| `/api/authz/forward-auth` | Traefik, Caddy | `X-Forwarded-Proto`, `-Host`, `-Uri`, `-Method` | a redirect |
| `/api/authz/auth-request` | ingress-nginx | `X-Original-URL`, `X-Original-Method` | 200 or 401 |

Mismatch fails closed and does not look like a configuration error. Pointing
Traefik at the nginx endpoint produces a bare **400** on every request to the
protected host, with the reason only in Authelia's log:

```
Error getting Target URL and Request Method:
missing required X-Original-URL header
```

The reverse — nginx at the forward-auth endpoint — produces a **500**, logged as
`failed to get target URL: missing host value`.

**Traefik's ingress-nginx compatibility provider still needs `forward-auth`.**
This is the trap worth naming twice. That provider reads
`nginx.ingress.kubernetes.io/*` annotations, so the annotation *looks* like an
nginx one and the IngressClass may still be called `nginx` — but it is Traefik
making the subrequest, so it sends `X-Forwarded-*` and nothing else. The
annotation dialect and the proxy asking are two separate questions.

## Why there is no `auth-signin`

On ingress-nginx it is required: `auth-request` answers 401 and nginx builds the
redirect from whatever `auth-signin` says, so it needs
`rm=$request_method` — ingress-nginx appends the return destination itself, and
what has to be passed explicitly is the original method.

On the forward-auth endpoint Authelia builds the redirect itself and supplies
both parameters, so the annotation has nothing left to do:

```
GET   →  302 https://auth.example.com/?rd=https%3A%2F%2Fhome.example.com%2F&rm=GET
POST  →  303 https://auth.example.com/?rd=https%3A%2F%2Fhome.example.com%2F&rm=POST
```

The method is preserved because Traefik sends `X-Forwarded-Method` and Authelia
reads it — which is strictly better than depending on `$request_method` being
interpolated by a proxy that is no longer nginx. Note the 303 on the POST: that
is the correct status for sending a POST to a login page, and it means the
failure mode this annotation used to guard against — a POST coming back as a GET
after sign-in, so the login succeeds and the action vanishes — does not arise.

Leaving a stale `auth-signin` in place is not fatal, but its value would contain
`$request_method` with nothing to expand it.

## Before you gate anything, check it is browser-only

Forward-auth answers an unauthenticated request with a redirect to a login
page. Anything that cannot follow that redirect and render a login form breaks —
and it breaks *silently*, as a sync that stops working rather than an error
anyone sees.

That rules out native and device clients: media player apps on phones and TVs,
note-sync desktop clients, anything speaking an API with its own credentials.
Those hosts stay on `bypass` and rely on their own authentication.

The rule to re-check every time: **if it is not a browser, it cannot pass this
gate.**

## Groups

`subject: ['group:admins']` in an access-control rule matches the `groups` list
in the users file. Group membership is a property of the user, not of the rule,
so adding somebody to a group is an edit to the `authelia-users` Secret.
