# The omnibox

Everything typed into the box arrives at `/search?q=` and leaves as a redirect.
Nothing is rendered in place: the target already knows how to search itself and
already knows who you are, so a bang works the day you add it.

## Precedence

First match wins, highest first.

1. **A bang**, at either end of the input.
2. **A resource**, if exactly one matches at the best available tier.
3. **The search engine.**

A bang beats a resource because explicit intent should not be overridden by
inference. If you typed `!t`, you want the archive, even if something is called
"t".

## Bangs

Configured in [the catalog](catalog.md), keyed without the `!`.

They work at **either end** of the input, because both get typed — you reach
for the bang first when you know where you are going, and last when you started
typing and then remembered:

```
!t wal archiving
wal archiving !t
```

Both go to the same place. Keys are case-insensitive. An unrecognised bang is
not a bang: `!zz thing` falls through and searches the web for `!zz thing`.

`!b` is registered automatically when bookmarks are enabled, since a knob whose
only correct setting is the default is not a knob. A catalog defining its own
`b` wins.

## Resource matching

Three tiers, tried in order, against every resource's name and aliases,
case-insensitively:

1. Exact
2. Prefix
3. Substring

**The first tier with any match wins outright.** An exact name is never beaten
by a prefix elsewhere: with resources called `Notes` and `Notesmith`, typing
`notes` goes to `Notes`.

**Ambiguity is never resolved by guessing.** If two resources match equally
well at the winning tier, the query falls through to web search instead. A
confident jump to the wrong application looks deliberate, and that is worse
than showing search results — so with `Photos` and `Photoprism`, typing `photo`
searches the web, and typing `photos` goes to Photos.

A resource matching on both its name and one of its aliases is one candidate,
not two.

## The address bar

The page publishes an OpenSearch descriptor at `/opensearch.xml`. Once your
browser has adopted it, all of the above works from the address bar in every
tab rather than only on the homepage — which is the difference between a page
you visit and a tool you use. See
[Set it up in your browser](../how-to/browser-setup.md).
