---
type: Tool Playbook
title: scan
description: Use this playbook when working with scan for the multi-stage aiscan pipeline across discovery, web probing, weak credentials, POC checks, and verification.
tags: [easm, pipeline]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# Scan

Scan is the multi-stage orchestration pipeline in aiscan.

Capabilities:

- combine discovery, web probing, weak credential checks, and POC execution
- produce discovered targets, services, web endpoints, fingerprints, weak credentials, POC matches, errors, and final stats
- expose pipeline capability names such as gogo portscan, spray web probing, zombie weakpass, and neutron POC
- optionally hand scanner output to an LLM agent for verification, sniper, or deep sub-skills when requested
- run quick or full profiles depending on depth needs

Common usage:

```bash
# Single target (-i)
scan -i 10.0.0.1 --mode quick
scan -i 10.0.0.0/24 --mode full
scan -i 10.0.0.1:8080 --mode quick
scan -i 10.0.0.1 --mode full --ports top1000
scan -i http://10.0.0.1:8080 --mode quick
scan -i https://example.com --mode quick

# Target list file (-l) — one target per line
scan -l /tmp/targets.txt --mode quick
scan -l /tmp/targets.txt --mode full --thread 4 --timeout 10

# AI features
scan -i 10.0.0.1 --verify=high
scan -i 10.0.0.1 --sniper
scan -i 10.0.0.1 --mode full --deep
scan -i 10.0.0.1 -j
```

**CRITICAL: `-i` vs `-l`**:
- `-i` is for one inline target per flag: IP, CIDR, IP:port, URL, or domain. Repeat `-i` for multiple inline targets.
- `-l` is for target list files (one target per line). **Always use `-l` when scanning from a file.**
- `scan -i /tmp/targets.txt` will FAIL — use `scan -l /tmp/targets.txt` instead.

**Input format for `-i`**:
- IP: `10.0.0.1`
- CIDR: `10.0.0.0/24`
- IP:port: `10.0.0.1:8080`
- URL (with port): `http://10.0.0.1:8080`
- Domain: `example.com` (scan discovers ports itself)
- Multiple inline targets: repeat `-i`, for example `scan -i 10.0.0.1 -i 10.0.0.2`
- **NOT** file paths — use `-l` for files.

Notes:

- `quick` uses gogo ports `all`, spray check/finger, spray crawl depth 2, weakpass checks, and fingerprint-based POC checks.
- `full` uses gogo ports `-` and adds spray plugins (common/bak/active) plus spray default-dictionary probing; crawl depth remains 2.
- Full builds additionally add katana crawling: `katana_crawl` in quick/full and `katana_deep` in full.
- Spray web capabilities run with recon enabled in both profiles.
- `--verify=<level>` triggers in-pipeline AI verification for loots at or above the specified priority threshold (low, medium, high, critical). Only loots meeting the threshold are sent to a verify sub-agent.
- `--sniper` asks an LLM agent to perform fingerprint vulnerability intelligence.
- `--deep` asks an LLM agent to perform browser-backed testing for discovered websites and fingerprint-based deep assessment.
- User intent decides whether scan output should be summarized, analyzed, validated, reported, or used to choose follow-up commands.

## AI Sub-Skills

The scan AI sub-skills are independent references:

- `aiscan://skills/scan/verify.md` - Active loot validation: probes targets to confirm or reject scanner leads
- `aiscan://skills/scan/sniper.md` — Vulnerability intelligence: searches for known CVEs based on discovered fingerprints
- `aiscan://skills/scan/deep.md` — Deep testing for discovered web endpoints and fingerprinted assets
- `aiscan://skills/scan/fuzz.md` — Internal parameter-review standard for high-value inputs; not a `scan` flag or standalone mode

## Related concepts

- Scan orchestrates [gogo](gogo.md), [spray](spray.md), [katana](katana.md),
  [zombie](zombie.md), and [neutron](neutron.md).
- [Passive discovery](passive.md) can seed targets before a scan, while
  [proton](proton.md) and [playwright](playwright.md) provide focused follow-up.
- Runtime support comes from [cyberhub](/runtime/search.md) for fingerprint-to-POC
  lookup, [proxy](/runtime/proxy.md) for routed execution,
  [mitm](/runtime/mitm.md) for traffic evidence, and
  [tmux](/runtime/tmux.md) for long-running sessions.
