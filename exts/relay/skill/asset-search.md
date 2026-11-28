# Asset search

One DSL queries every asset-search backend behind the relay. Each backend
translates it internally; the relay fails over between them (or fans out and
merges) and returns normalized assets. The relay does not say which backend
answered, and a backend cannot be picked. Run `relay search-assets -h` for every field.

## Unified search — `relay search-assets`

```bash
relay search-assets 'domain="example.com" && (port=443 || port=8443)' --size 50
relay search-assets 'icp="京ICP备12345678号"' --route-mode fanout
```

| Field | Default | Notes |
| --- | --- | --- |
| query (the words) | required | DSL below |
| `--size` / `--page` | `50` / `1` | Results per page, page from 1. `--size` at most `10000` (the key's own `max_size` may be lower), `--page` at most `100000` |
| `--group` | `asset_search` | Routing group |
| `--route-mode` | group default | `rotate` (one backend answers, the next is tried if it fails) or `fanout` (every member that can run the query is queried and the results merged; the key's daily quota for the group is counted once). Another value is `400 INVALID_ROUTE_MODE` |
| `--strict` | off | Off: a backend without an exact-match operator runs `==` as contains and says so in `warnings`. On: such a backend is skipped |
| `--format` | `flat` | Extension formats registered on this relay may also return typed `entities`; ask the operator which are enabled. An unknown one is `400 INVALID_FORMAT` |
| `--cache=false` | cached 24h | Only when freshness matters |

## DSL

- Compare: `=` contains, `==` exact, `!=` exclude.
- Combine: `&&` / `AND`, `||` / `OR`, parentheses. `&&` binds tighter than `||`.
- Strings in double or single quotes; numbers may be bare (`port=443`).
- `port` (1–65535), `status_code` (100–599) and `asn` (digits, an `AS` prefix
  is accepted) take a number only.
- Limits: 4096 characters, parentheses nested 32 deep, 128 comparisons.

| Field | Example | Notes |
| --- | --- | --- |
| `ip`, `cidr` | `ip="1.2.3.4"`, `cidr="1.2.3.0/24"` | |
| `port` | `port=443` | |
| `domain`, `host` | `domain="example.com"` | Some backends map `domain` approximately (see `warnings`) |
| `title`, `body`, `header` | `title="登录"` | |
| `protocol` | `protocol="https"` | |
| `product` / `app` | `app="nginx"` | Fingerprint names differ per platform |
| `country` | `country="CN"` | ISO code |
| `asn`, `org` | `asn=4134`, `org="Chinanet"` | |
| `cert` | `cert="example.com"` | |
| `status_code` | `status_code=200` | |
| `icp` | `icp="京ICP备12345678号"` | Not every backend has ICP data |

Use these fields only; never write a vendor's native syntax. Anything else
(`org:"x"`, an unknown field such as `domain.suffix=`) fails with
`400 INVALID_DSL`, and the message lists the valid fields. A backend that
cannot express a field is never queried with the field dropped: it is
skipped, and when no member of the routing group can run the query the
request fails with `400 DSL_UNSUPPORTED_BY_GROUP`.

## Reading the answer

- Assets are deduplicated by `canonical_key` (`ip:port:protocol`). When
  several backends return the same service, `domains` / `urls` hold every
  value seen. Empty fields are omitted.
- `protocol` is normalized (`https` for any TLS web service) and `url` carries
  the port when it is not the default one.
- Always read `warnings` (omitted when there are none): they note approximate
  field mappings and skipped backends.
- `total` is the upstream match count, `count` the assets in this answer.
  Page with `--page` while `page * size < total` and the task still needs more.
- `failover_count` tells whether a failover happened; `cache_hit: true` means
  no upstream was called.

## Preflight — `relay explain-search`

Same fields as `search-assets`. Validates the query against the routing group
without calling any upstream: no daily quota and no upstream credits are
spent (the call still counts against the rate limit). Use it when unsure
whether a query or field is supported. `supported: true` means at least one
member of the group the key may use can run the query, and `warnings` lists
their caveats; with `supported: false`, `warnings` says why none can. It
needs the same scope as the search it previews.
It does not check the daily quota, `max_size` or upstream availability, so a
query that explains fine can still get `429` or `503` on `search-assets`.

## Single host — `relay lookup-host <ip>`

```bash
relay lookup-host 1.2.3.4
relay lookup-host 2001:db8::1 --group asset_search
```

`<ip>` is one IPv4 or IPv6 address: a CIDR or a hostname is `400 INVALID_IP`.
Optional `--group`, `--route-mode`, `--format`. The answer has the shape of
`search-assets`. Lookups are not cached and count once against the key's
daily quota for the group. A key without search or host scope for the group
gets `403 SCOPE_DENIED`, not an empty result.

## Errors

- `400 INVALID_DSL`: syntax error, unknown field, a non-numeric value for a
  numeric field, or a query over the limits; the message says which.
- `400 INVALID_SIZE` / `INVALID_PAGE`: lower `--size` or `--page`.
- `400 DSL_UNSUPPORTED_BY_GROUP`: no backend in the routing group can run the
  query; remove the unsupported field or drop `--strict`.
- `429 MAX_SIZE_EXCEEDED`: `--size` is above the key's limit; lower it.
- `429 QUOTA_EXCEEDED`: the key's daily budget for this group is spent; stop and
  report.
