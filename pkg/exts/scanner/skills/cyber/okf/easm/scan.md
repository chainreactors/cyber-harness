---
type: Tool Playbook
title: scan
description: Use this playbook when working with scan for the multi-stage cyber pipeline across discovery, web probing, weak credentials, POC checks, and verification.
tags: [easm, pipeline]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# Scan

Scan is the multi-stage orchestration pipeline in cyber.

Capabilities:

- combine discovery, web probing, weak credential checks, and POC execution
- produce discovered targets, services, web endpoints, fingerprints, weak credentials, POC matches, errors, and final stats
- expose pipeline capability names such as gogo portscan, spray web probing, zombie weakpass, and neutron POC
- hand vulnerabilities and weak passwords to an LLM agent for verification when enabled, or fingerprints for sniper research when requested
- run quick or full profiles depending on depth needs

Common usage:

```bash
# Single target (-i)
scan -i 10.0.0.1 --mode quick
scan -i 10.0.0.0/24 --mode full
scan -i 10.0.0.1:8080 --mode quick
scan -i 10.0.0.1 --mode full --ports top3
scan -i http://10.0.0.1:8080 --mode quick
scan -i https://example.com --mode quick

# Target list file (-l) — one target per line
scan -l /tmp/targets.txt --mode quick
scan -l /tmp/targets.txt --mode full --thread 4 --timeout 10

# AI features
scan -i 10.0.0.1 --verify=on
scan -i 10.0.0.1 --sniper
scan -i 10.0.0.1 --mode full
scan -i 10.0.0.1 -j
```

`scan -j` enables scan-level JSON Lines output. It is not the same flag as direct `gogo -j`, which takes a previous-results input file. Likewise, `--mode full` is a scan profile, not a direct `gogo -p full` preset.

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
- Full builds additionally add Katana crawling: `katana_crawl` in quick/full uses the standard HTTP engine, while `katana_deep` in full uses pure headless rendering and emits browser-only requests and SPA navigation.
- `katana_deep` shares Cyber's browser discovery order: `CYBER_BROWSER_PATH`, installed Chrome/Chromium/Edge, then Rod's cache/download fallback.
- Spray web capabilities run with recon enabled in both profiles.
- `--verify=on|off` controls AI verification of all vulnerability and weak-password findings. The execution node defaults to on when it has a model, otherwise off. Fingerprints are excluded.
- `--sniper` asks an LLM agent to perform fingerprint vulnerability intelligence.
- User intent decides whether scan output should be summarized, analyzed, validated, reported, or used to choose follow-up commands.

## AI Sub-Skills

The scan AI sub-skills are independent references:

- `cyber://skills/scan/verify.md` - Active loot validation: probes targets to confirm or reject scanner leads
- `cyber://skills/scan/sniper.md` — Vulnerability intelligence: searches for known CVEs based on discovered fingerprints
- `cyber://skills/scan/deep.md` — Deep testing for discovered web endpoints and fingerprinted assets
- `cyber://skills/scan/fuzz.md` — Internal parameter-review standard for high-value inputs; not a `scan` flag or standalone mode

## Related concepts

- Scan orchestrates [gogo](gogo.md), [spray](spray.md), [katana](katana.md),
  [zombie](zombie.md), and [neutron](neutron.md).
- [Passive discovery](passive.md) can seed targets before a scan, while
  [proton](proton.md) and [playwright](cyber://skills/runtime/playwright.md) provide focused follow-up.
- Runtime support comes from [cyberhub](cyber://skills/runtime/search.md) for fingerprint-to-POC
  lookup, [proxy](cyber://skills/runtime/proxy.md) for routed execution,
  [mitm](cyber://skills/runtime/mitm.md) for traffic evidence, and
  [tmux](cyber://skills/runtime/tmux.md) for long-running sessions.
