---
type: Tool Playbook
title: gogo
description: Use this playbook when working with gogo for host, port, service, banner, fingerprint, or vulnerability-hint discovery.
tags: [easm, discovery]
status: stable
generated: { by: process:okf-migration, at: 2026-08-02T00:00:00Z }
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
gogo -i 10.0.0.1 -p top100
gogo -i 10.0.0.0/24 -p 80,443,8080
gogo -i 10.0.0.1,10.0.0.2 -p all
gogo -l /tmp/targets.txt -p top100
```

Notes:

- `-i` accepts IP, CIDR, or comma-separated IPs. **NOT** `ip:port` — bare `10.0.0.1:8080` will fail with "Parse IP Failed". Use `-i 10.0.0.1 -p 8080` instead.
- `-l` reads a target file (one IP/CIDR per line).
- `-p` is gogo ports (`top100`, `top1000`, `all`, `-` for all 65535, or `80,443,8080`).
- Fingerprints and vuln hints are evidence leads; user intent decides whether to summarize, analyze, verify, compare, or plan follow-up work.
