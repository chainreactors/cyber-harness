# Proxy pool

The relay pools exit IPs in two regions: `cn` (domestic) and `global`
(overseas, including IPv6 subnets). Use them when a target blocks
the current egress, or the task needs a specific country, province, city or
carrier. Exits are only reachable through the relay tunnel; the relay never
returns an upstream proxy address or credentials.

## Get a tunnel URL — `relay acquire-proxy`

```bash
relay acquire-proxy --region global --country US --protocol socks5 --session-id task01
relay acquire-proxy --region cn --area 广东 --isp telecom --session-id task02
```

→ `proxies[]` with `lease_id`, `session_id`, `region`, `protocol`,
`tunnel_url`, `exit_ip`, `location`, `isp`, `expires_at`.

- `tunnel_url` is a ready proxy URL, e.g.
  `socks5://<relay key>:global.us.sticky.task01@relay.example:18081`. Its
  username is this host's relay key: pass the URL straight to the tool that
  needs it (`curl -x "<tunnel_url>" …`), and never put it in a report or a
  file. Change `socks5://` to `socks5h://` so hostnames are resolved at the
  exit.
- Keep `lease_id` for rotate, switch and check.
- `exit_ip`, `ip_version`, `location` and `isp` are omitted when the upstream
  did not report them; `relay check-proxy` finds the exit IP.

| Field | Default | Notes |
| --- | --- | --- |
| `--region` | `cn` | `cn` or `global` |
| `--country`, `--state` | – | `global` only: ISO country code, state code within it |
| `--area`, `--isp` | – | `cn` only: Chinese province or prefecture-level city name; `telecom`, `unicom` or `mobile` |
| `--protocol` | – | `http` or `socks5`: scheme of `tunnel_url`, also asked of the upstream exit. Both work on the tunnel port |
| `--session-id` | generated | Sticky session id: letters, digits, `_` and `-`, at most 64 characters (61 with `--count` above 1). The same id, region and filter return the same lease while it is active |
| `--count` | `1` | Separate exits, each its own session (`<id>-1` … `<id>-N`); at most 20 |
| `--ip-strategy` | `auto` | `auto`, `prefer_ipv6`, `ipv6_only` or `ipv4_only` |
| `--refresh` | off | Pull a fresh upstream IP instead of reusing a pooled one |
| `--group` | region default | Pin a routing group |

Location filters are strict: you only ever get an exit in that location, never
a fallback elsewhere; a filter no upstream can serve is
`400 PROXY_GEO_UNSUPPORTED`. For `cn`, `--area` takes one province (`广东`) or
prefecture-level city (`深圳`) by its Chinese name. Check the actual exit with
`relay check-proxy` if the exact city matters.

## Tunnel options

The password part of `tunnel_url` holds the routing options, dot-separated in
any order. Acquire again with other fields rather than editing them by hand;
the tokens are:

| Token | Meaning |
| --- | --- |
| `cn`, `global` | Region |
| two letters, e.g. `us` | Exit country (`global` only) |
| `state.<code>` | State within the country (`global` only) |
| `area.<province or city>` | Exit province or city (`cn` only) |
| `isp.<telecom\|unicom\|mobile>` | Carrier (`cn` only) |
| `sticky.<id>` | Keep the same exit for this session id |
| `rotate` | No session: each new connection may use a different exit |
| `prefer_ipv6`, `ipv6_only`, `ipv4_only` | IP stack |

Use one sticky session per multi-step flow (login, then actions) so the
target sees one IP, and a new id per independent task. A session keeps its
exit until `expires_at` (by default 5 minutes after the lease), unless the
exit fails first. After `expires_at` the same id may come back on a different
IP: check `exit_ip` again before continuing a long flow.

The tunnel speaks HTTP proxy (CONNECT and plain `http://`) and SOCKS5
(CONNECT only, no UDP). Tunnel status codes on the HTTP proxy: `407` the key
is rejected; `429` a limit (`RATE_LIMITED`: wait; `QUOTA_EXCEEDED` /
`REQUEST_LIMIT_EXCEEDED`: the budget is spent); `403` any other refusal
(scope, whitelist, session id, location, destination); `502` no exit could be
dialed. SOCKS5 cannot report a reason: validate a new location filter with
`acquire-proxy` first. The pool is for public targets: some exits refuse
private, loopback and link-local destinations.

## Rotate, switch, check, status

- `relay rotate-proxy --lease-id <id> --reason blocked` moves a session to
  another exit; `relay switch-proxy` does the same but pulls a new exit from
  the upstream by default. Instead of `--lease-id` you may send
  `--session-id` with its `--region`. `--reason` is `blocked`, `timeout`,
  `auth_failed` or `manual`. `--force-upstream` (or `--force-upstream=false`
  on a switch) overrides where the new exit comes from. Add `--country` (or
  `--state` / `--area` / `--isp`) to move the session elsewhere. The answer
  has a new `lease_id`; the `tunnel_url` you hold keeps working unless the
  location changed.
- A different public IP is not guaranteed: compare `exit_ip`, or run
  `relay check-proxy`. A failed rotate or switch can leave the session
  without a lease: acquire the session again (same id and location) and use
  the lease in that answer.
- `relay check-proxy <lease_id>` probes the lease's exit: `reachable`,
  `latency_ms`, and when known `exit_ip`, `country`, `region_name`, `city`,
  `isp`.
- `relay proxy-status` shows `tunnel_enabled`, `tunnel_public_addr` and pool
  health per region the key may use. `ready_nodes: 0` is not an error: exits
  are pulled on demand.

## Tunnel whitelist

`relay get-proxy-whitelist` shows the key's tunnel source-IP whitelist and the
IP the relay sees (`client_ip`); an empty list adds no restriction.
`relay add-proxy-whitelist` with no field adds this host's IP, or
`--ip 1.2.3.4` / `--ips 1.2.3.0/24` (`--replace` sets the whole list).
`relay remove-proxy-whitelist 1.2.3.4` removes entries. Changing it needs the
`proxy:whitelist_sync` scope. A sub-key is also held to its master key's
whitelist, which these calls cannot change.

## Errors

- `503 PROXY_TUNNEL_DISABLED`: the tunnel is off; proxies are unavailable.
  `400 PROXY_UNAVAILABLE`: this relay has no proxy pool; stop and report.
- `400 PROXY_GEO_INVALID`: bad location filter; check the message/hint for which field failed validation and fix the filter.
- `400 PROXY_SWITCH_UNSUPPORTED`: the upstream cannot give this session a
  different IP. Do not retry the switch; use a new session id instead.
- `400 INVALID_SESSION_ID` / `INVALID_LEASE_ID`: fix the id.
- `403 BINDING_CONFLICT`: the key is bound to a fixed proxy route; drop
  `--group`.
- `429 PROXY_SWITCH_LOCKED`: you switched moments ago; wait the `retry after`
  time, then retry once.
- `502 PROXY_UPSTREAM_ERROR`: retry once (after a failed rotate or switch,
  acquire the session again first).
- `403 TUNNEL_IP_NOT_ALLOWED`: this host's IP is not on the key's tunnel
  whitelist. Run `relay add-proxy-whitelist` if the key may change it;
  otherwise stop and report.
- `403 LEASE_OWNER_MISMATCH` / `404 LEASE_NOT_FOUND`: acquire a new lease.

## Rules

- Rotate or switch only when an exit is actually blocked or failing. The
  cooldown is per key and region (default 15 s) and rotate and switch share
  it. A new session id takes another exit without the cooldown; prefer it
  when you just need a different exit for a new task.
- Acquire, rotate, switch and every tunnel connection count against the
  daily quota; check, status and the whitelist calls do not.
- `403 SCOPE_DENIED` for a region means the key has no scope for it; do not
  try the other region as a workaround.
