# Design

Why Scryglass is shaped the way it is, including several decisions where the
obvious choice turned out to be wrong against a real cluster.

## One entity, not three

A dashboard tile, an uptime monitor and a bookmark are the same record written
down three times: a URL, a name, and — sometimes — a health check.

> A **resource** is a URL, a name, a category, and an *optional* check.

A bookmark is a resource with no check. A tile is one with a heartbeat. One
list, rendered two ways. That collapse is the whole product; everything else is
presentation.

## Discovery over a kept list

The tile wall is built from Ingress objects rather than from a list you
maintain.

This is not about saving typing. Every service already declares its canonical
address in an Ingress, so a hand-kept tile list is a second inventory that has
to be remembered — and the failure mode of a second inventory is that it goes
quietly out of date until nobody trusts the page. That is what happened to the
dashboard Scryglass replaced.

The consequence worth accepting: what the wall shows is what the cluster says,
and corrections are annotations on the thing being described rather than
entries in a list about it.

## Probe the Service, not the public address

A check targets the backing Service — `service.namespace.svc` — rather than the
Ingress host.

Probing the public address of anything behind an authenticating proxy returns
the login page's redirect. That proves the ingress and the identity provider
are alive and says **nothing** about the application: every gated host would
look identically healthy whatever state it was in.

It follows that a probe bypasses the ingress, and therefore bypasses whatever
NetworkPolicy admits the ingress. An application with a restrictive ingress
policy will show as down while being perfectly healthy. Widening the policy is
one option; `check: off` and accepting a link with no status is the other, and
a tight policy can be worth more than a status dot.

## What counts as up

**Anything under 500.** Plenty of applications answer `/` with 401, 403 or 404
and are working perfectly. The question is whether the process is listening and
serving, and a 404 proves that as conclusively as a 200 does. A 5xx is the one
class that means the application itself is unwell.

**Redirects are answers, not something to follow.** Following one would probe
whatever it points at — for a gated application, the login page's health rather
than its own.

**Certificate verification is off for probes, and only for probes.**
Applications commonly present self-signed certificates on Service DNS names
they were not issued for; verifying would report every one of them as down. The
question is "is this answering", not "is it who it claims to be", and the
connection does not leave the cluster.

**Retired resources are never probed at all**, rather than probed and ignored.
A wall of red for things you switched off yourself is how a dashboard stops
being read.

## No alerting

The question Scryglass answers is *what is red when I look at the page*, and
stopping there is deliberate.

It keeps this one small binary instead of a second application to operate, and
it has a second-order effect that shows up in the design: with no alerting
there is no consumer for long history, so the status bar keeps a couple of
hours of samples rather than days.

If you want to be told when something breaks while you are not looking, run
something that does that. This is not it, and trying to make it that is how it
becomes the thing it replaced.

## The omnibox is a router

The search box redirects. It does not render results from other systems in
place.

The targets already know how to search themselves and already know who you are,
so a bang is a redirect and nothing needs integrating — a new one works the day
you add it. Federating results inline is a real feature and a much larger one;
it is deliberately unbuilt until the redirects prove insufficient.

The same reasoning shapes what the box does when it is unsure. Ambiguity falls
through to web search rather than picking a winner, because a confident jump to
the wrong application looks deliberate.

## The page is a page

Server-rendered HTML, no framework, no build step, one request. The stylesheet
is inlined because a second request for CSS is a second round trip before
anything is painted, and this is a new tab page where the entire budget is one
paint.

Live tile updates arrive over server-sent events carrying no payload — the
client re-fetches the tile fragment. One renderer rather than two, and a client
that reconnects after missing events gets current state rather than a replay it
cannot apply.
