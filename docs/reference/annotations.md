# Ingress annotations

Per-service overrides for discovered tiles. Read within one discovery interval,
so **nothing needs redeploying**.

The prefix is `scryglass.local` by default and is configurable with
`SCRYGLASS_ANNOTATION_PREFIX`, because it is written into other people's
manifests.

| Annotation | |
|---|---|
| `name` | Tile name. Defaults to the Ingress object's name, title-cased — so `searxng-ingress` becomes "Searxng Ingress". |
| `group` | Section heading. **No default:** namespace is the obvious candidate and a bad one, since one application per namespace produces a section per tile. |
| `icon` | Tile glyph. |
| `probe` | A path appended to the derived check URL, or a complete `http://`/`https://` URL replacing it entirely. |
| `retired` | `true` dims the tile and stops probing it. |
| `hide` | `true` removes it from the wall. |

Booleans accept `true`, `yes` or `1`.

## How a check is derived

The probe URL comes from the Ingress backend, not from the public host, and the
difference is the whole point — see [Design](../explanation/design.md).

1. The backing Service name and namespace give the authority:
   `service.namespace.svc`.
2. The port comes from the Ingress backend. A **named** port is resolved by
   listing Services; if it cannot be resolved, the resource gets **no check at
   all** rather than a guess, because a guess would probe a closed port and
   report a healthy application as down.
3. The scheme is inferred from the port: a name containing `https`, or ports
   443, 8443 and 10443, select HTTPS.
4. The path is `/`, or whatever `probe` says.

Steps 3 and 4 are where inference fails, and the `probe` annotation taking a
complete URL is the escape hatch. Some servers speak TLS on their plaintext
port and close a plain HTTP connection without answering, which surfaces as
`EOF` and looks exactly like being down.

## Example

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: lychee
  annotations:
    scryglass.local/name: "Photos"
    scryglass.local/group: "media"
    scryglass.local/probe: "/api/v2/version"
spec:
  rules:
    - host: photos.example.com
      # …
```
