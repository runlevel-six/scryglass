# Protecting an Ingress with Authelia

These annotations are **not applied by this kustomization**. Each protected app
owns its own Ingress, usually in its own namespace and often in another
repository, so gating one is a deliberate edit to that app's manifest.

Add to the Ingress you want to protect:

```yaml
metadata:
  annotations:
    nginx.ingress.kubernetes.io/auth-url: >-
      http://authelia.authelia.svc.cluster.local:9091/api/authz/forward-auth
    nginx.ingress.kubernetes.io/auth-signin: >-
      https://auth.example.com/?rd=$target_url
    nginx.ingress.kubernetes.io/auth-response-headers: >-
      Remote-User,Remote-Name,Remote-Email,Remote-Groups
    nginx.ingress.kubernetes.io/auth-snippet: |
      proxy_set_header X-Forwarded-Method $request_method;
```

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
