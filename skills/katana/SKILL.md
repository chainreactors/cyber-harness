---
name: katana
description: Use katana for deep web crawling with full parameter discovery. Produces URLs with query strings, form targets, and JS endpoints that spray crawl strips.
internal: true
---

# Katana — Parameter-Aware Web Crawler

Katana is a web crawler from ProjectDiscovery that preserves full URLs including query parameters, form actions, and JavaScript-discovered endpoints. Use it when you need to enumerate the attack surface of a web application beyond path discovery.

## When to Use

- After `scan` or `spray` discovers web targets — katana fills in the parameter layer that spray crawl strips
- Before fuzzing — katana provides the target URLs with parameters to test
- When JavaScript-heavy applications need deeper endpoint extraction (`-jc` flag)

## Relationship to Spray Crawl

Spray crawl discovers paths and fingerprints. Katana discovers parameterized URLs. They complement each other:

- `spray --crawl` → paths, fingerprints, tech stack → feeds into neutron POC
- `katana` → full URLs with `?key=value`, form targets, API endpoints → feeds into manual fuzzing

## Common Usage

```bash
katana -u https://target.com -d 3 -jc
katana -u https://target.com -d 2 -jsonl
katana -u https://target.com -f qurl
katana -list urls.txt -d 2 -jc -timeout 60
```

## Useful Filters

- `-f qurl` — only output URLs that contain query parameters
- `-f kv` — output key=value pairs extracted from URLs
- `-f path` — output only paths
- `-em php,asp,jsp` — match specific extensions
- `-ef css,js,png,jpg,gif,svg,woff` — filter out static assets

## Output

Default output is one URL per line. Use `-jsonl` for structured JSON with request/response details. Agent should pick the format that fits the task — plain URLs for quick review, JSON for parameter extraction.
