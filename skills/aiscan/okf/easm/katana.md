---
type: Tool Playbook
title: katana
description: Use katana for deep web crawling with full parameter discovery. Produces URLs with query strings, form targets, and JS endpoints that spray crawl strips.
tags: [easm, web, crawling]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# Katana — Parameter-Aware Web Crawler

Katana is a web crawler from ProjectDiscovery that preserves full URLs including query parameters, form actions, and JavaScript-discovered endpoints. Use it when you need to enumerate the attack surface of a web application beyond path discovery.

## When to Use

- After `scan` or `spray` discovers web targets — katana fills in the parameter layer that spray crawl strips
- Before fuzzing — katana provides the target URLs with parameters to test
- When JavaScript-heavy applications need deeper endpoint extraction (`-jc` flag)
- When visible attack surface is thin — use katana as the batch candidate generator instead of manually extracting JS URLs one by one

## Relationship to Spray Crawl

Spray crawl discovers paths and fingerprints. Katana discovers parameterized URLs. They complement each other:

- `spray --crawl` → paths, fingerprints, tech stack → feeds into neutron POC
- `katana` → full URLs with `?key=value`, form targets, API endpoints → feeds into manual fuzzing

Do not feed every discovered URL back to the model one by one. Save or consume katana output as a batch, group by host/path/parameter shape, then select high-value candidates for authorization, unauthenticated access, upload, GraphQL, or injection validation.

## Common Usage

```bash
katana -u https://target.com -d 3 -jc
katana -u https://target.com -d 2 -jsonl
katana -u https://target.com -f qurl
katana -u https://target.com -d 3 -jc -jsonl
katana -list urls.txt -d 2 -jc -timeout 60

# Rendered browser crawling
katana -u https://target.com -hl -d 2 -jsonl
katana -u https://target.com -hh -d 2 -jsonl
katana -u https://target.com -hl --chrome-ws-url ws://127.0.0.1:9222/devtools/browser/<id>
```

## Browser Modes and Reuse

- Standard Katana crawling does not launch a browser. `-jc` parses JavaScript responses but does not render the application.
- `-hl` runs pure headless crawling and captures browser requests, dynamic navigation, forms, and rendered interactions.
- `-hh` combines HTTP crawling with browser rendering. Prefer `-hl` when browser network events and SPA navigation must be emitted as results.
- The full scan profile's `katana_deep` capability uses pure headless crawling; `katana_crawl` remains the lower-cost standard crawler.

AIScan resolves a browser in this order:

1. Katana `--chrome-ws-url` (`-cwu`) for an explicitly managed running process.
2. Katana `--system-chrome-path` (`-scp`) for an explicitly selected executable.
3. `AISCAN_BROWSER_PATH` shared by AIScan Playwright, nuclei headless replay, and Katana.
4. Installed Chrome, Chromium, or Edge in PATH or the standard OS install locations.
5. Rod's existing browser cache, with its first-use download only when the cache is absent.

Automatic reuse means the executable is shared while each engine starts an isolated process/profile. AIScan does not automatically attach to a user's running browser. Use `--chrome-ws-url` only when process-level reuse is intentional.

## Useful Filters

- `-f qurl` — only output URLs that contain query parameters
- `-f kv` — output key=value pairs extracted from URLs
- `-f path` — output only paths
- `-em php,asp,jsp` — match specific extensions
- `-ef css,js,png,jpg,gif,svg,woff` — filter out static assets

## Output

Default output is one URL per line. Use `-jsonl` for structured JSON with request/response details. Agent should pick the format that fits the task — plain URLs for quick review, JSON for parameter extraction.

## Related concepts

- Run Katana after the [scan pipeline](scan.md), [spray](spray.md), or
  [passive discovery](passive.md) identifies web targets.
- Katana complements Spray by preserving parameters; use
  [playwright](playwright.md) when discovered routes require JavaScript or
  interactive validation.
