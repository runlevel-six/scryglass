# The catalog file

A YAML file naming your search engine, your bangs, and any resources you want
to write down by hand. Its path comes from `SCRYGLASS_CATALOG`.

It is read once at startup. Deploy it as a generated ConfigMap so that editing
it produces a new name and rolls the Deployment — an apply that changes no spec
triggers no rollout, which turns a config change into "apply, then restart by
hand".

Unknown keys are an error, not something to ignore. A misspelled key would
otherwise be a resource that never appears and a file that looks correct.

## Format

```yaml
# Where a query goes when nothing else claims it. Required.
search:
  name: Web
  url: "https://search.example.com/search?q={query}"

# Prefixes that route somewhere specific.
bangs:
  - key: t
    name: Archive
    url: "https://archive.example.com/search?q={query}"

# Things to jump to.
resources:
  - name: Photos
    url: https://photos.example.com
    group: media
    aliases: [gallery, pics]
```

`{query}` is replaced with the percent-encoded query. Both `search.url` and
every `bangs[].url` must contain it; startup fails if one does not.

Bang keys are written **without** the leading `!`.

## Resource fields

| Field | |
|---|---|
| `name` | Required. What the tile says and what the omnibox matches. |
| `url` | Required. Where it goes. Also the key discovery matches on. |
| `group` | Section heading on the wall. Ungrouped resources collect at the end. |
| `aliases` | Additional names the omnibox matches — what you actually type. |
| `icon` | Tile glyph. |
| `check` | URL to probe. Empty means never probed, which is what makes a bookmark a bookmark. |
| `retired` | Dim rather than alarm. Never probed. |

## How this file corrects discovery

An entry here **corrects** a discovered resource rather than replacing it.
Entries are matched on `url`, and the file wins on any field it sets while
discovery supplies the rest.

So this is enough to rename a discovered service and put it in a group:

```yaml
resources:
  - name: Photos
    url: https://photos.example.com
    group: media
```

— and the health check, icon and aliases discovery worked out are all kept.

This was not the original behaviour and the change is worth knowing about. A
whole-record override discarded the check, and the symptom was invisible: the
tile still rendered, and simply never went red again.

`retired` is ORed rather than overridden, because a bool cannot distinguish
`false` from unset. Either source saying a thing is retired is enough.

To deliberately **not** probe something discovery found:

```yaml
check: off
```

## Ordering

There is no separate ordering mechanism. The catalog's order is the wall's
order: entries here come first, in the order you wrote them, then discovered
resources the file did not claim. Groups appear in the order their first member
appears.

To move a discovered tile, name it here in the position you want.
