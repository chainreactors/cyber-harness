---
type: Tool Playbook
title: gogo
description: Use this playbook when working with gogo for host, port, service, banner, fingerprint, or vulnerability-hint discovery.
tags: [easm, discovery]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# Gogo

Gogo is the host and service discovery tool in aiscan.

Capabilities:

- discover live hosts and open ports from IP, CIDR, host, or target files
- identify protocols, services, banners, TLS hints, and response metadata
- match service and web fingerprints from the embedded finger engine
- surface focus fingerprints and vuln hints as leads for later analysis
- produce scan summary data such as alive count, total count, timing, and errors

Common usage:

```bash
gogo -i 10.0.0.1 -p top2
gogo -i 10.0.0.0/24 -p 80,443,8080
gogo -i 10.0.0.1,10.0.0.2 -p all
gogo -l /tmp/targets.txt -p top2
```

Notes:

- `-i` accepts IP, CIDR, or comma-separated IPs. **NOT** `ip:port` — bare `10.0.0.1:8080` will fail with "Parse IP Failed". Use `-i 10.0.0.1 -p 8080` instead.
- `-l` reads a target file (one IP/CIDR per line).
- `-p` is gogo ports: in the current resource, presets include `top1` / `top2` / `top3` (default `top1`, widening coverage), `all` (every preset port), and `-` for all 65535; resource-defined tags/aliases, ranges like `10000-10100`, and explicit `80,443,8080` are also accepted. A name such as `common` is valid only when the current resource defines that tag/alias. Do not infer names such as `top100` / `top1000` / `top2k` / `top12k` / `full` from another release: a name absent from the current resource is passed through as a literal port/service name, not expanded as top-N; it can therefore produce `total ports: 1` and no useful results.
- If the resource version is uncertain, run `gogo -P port` before choosing a preset; do not substitute an old QuickReference or remembered preset names for the runtime list.
- Direct gogo output/input flags are distinct: `-o <format>` is the console format, `-f <path>` is the output filename, `-O <format>` is the file format, value-bearing `-j <json-file>` reads a previous-results JSON input, and `-t/--thread <number>` sets threads. Use `-o jl` for console JSON Lines or `-f <path> -O jl` for a JSON Lines file; `-j 16` is a file named `16`, not a thread count; `-f json` names a file `json`; and a path is not an `-o` format. Do not use valueless `-j` as direct gogo output syntax.
- The `total ports: 1` log is the length of the normalized port plan. It does not mean that a complete port scan ran.
- A number reported after preset expansion (for example, 253 ports) is an observed plan size, not another `-p` preset name.
- Fingerprints and vuln hints are evidence leads; user intent decides whether to summarize, analyze, verify, compare, or plan follow-up work.

## Related concepts

- The [scan pipeline](scan.md) orchestrates gogo during target discovery.
- Discovered services feed [spray](spray.md) for HTTP probing and
  [zombie](zombie.md) for authorized credential checks.
- Fingerprints can be resolved through [cyberhub](/runtime/search.md) and
  validated with [neutron](neutron.md) templates.
