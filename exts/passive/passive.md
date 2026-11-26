---
type: Tool Playbook
title: passive
description: Use passive to expand domains/ICPs into cyberspace assets like IPs, URLs, ports via the configured uncover sources. Run before active scanners (gogo, spray, katana).
tags: [easm, recon]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# Passive

`passive` is a cyber command backed by `uncover` for cyberspace search. Pick a source with `-s`.

## Sources

Only sources configured by the host are available. Run `passive -h` first: its
`Available now:` line is the complete list of sources for this session, and its
usage examples show their native query syntax. Do not try a source that is not
listed there; credentials and source enablement belong to the host configuration,
not to command arguments.

## When to Use

- **Domain/ICP → IPs, URLs, ports**: query an available cyberspace source.
- Feed output into `gogo`, `spray`, `katana`, or `neutron` for active scanning.

Skip `passive` when the user already provided concrete IPs or private ranges.

## Usage

```bash
passive -h
passive -s <source> '<source-native query>'
```

The positional argument is a single source-native query string; quote it as one
shell word. Each source keeps its own query language.

## Output

A JSON array of assets. Every row carries `ip`, `port`, and `url`; rows also carry
`host` or `domain`, and sources with richer data add fields such as `title`, `icp`,
`status`, `company`, `frame`, or `source`.

```json
[{"ip":"192.0.2.4","port":"443","url":"https://example.com","domain":"example.com","title":"Example"}]
```

## Typical Pipeline

1. `passive -s <source> '<query for the target domain or ICP>'` → get IPs/URLs
2. `gogo` / `spray` / `katana` → active scan discovered assets

## Notes

- Some sources restrict query shapes or regions; `passive -h` notes the known
  restrictions, and errors name the available alternatives.
- ICP data may lag reality; treat domain mapping as leads, not authoritative.

## Related concepts

- Passive discovery supplies targets to the [scan pipeline](cyber://skills/cyber/okf/easm/scan.md),
  [gogo](cyber://skills/cyber/okf/easm/gogo.md), [spray](cyber://skills/cyber/okf/easm/spray.md), and [katana](cyber://skills/katana/katana.md).
- Use the [proxy runtime](cyber://skills/runtime/proxy.md) when a configured discovery source
  requires routed access.
