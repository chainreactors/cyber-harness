---
type: Tool Playbook
title: curl
description: Use this playbook for targeted HTTP requests with curl — a pure-Go, browser-naturalized, evidence-first client that replaces ad-hoc HTTP probing.
tags: [easm, web]
status: stable
---

# curl

curl is aiscan's pure-Go HTTP client. It exposes a curl-shaped flag surface, so
it is used exactly like the system tool, while every request routes through the
runner proxy — attributed by tool-call id and captured as HTTP evidence — and
carries a browser-shaped User-Agent and header set by default instead of
announcing itself as automated tooling.

Capabilities:

- send one HTTP request with an explicit method, headers, and body
- submit form data (`-d`) or fold it into the query string (`-G`)
- follow redirects (`-L`), with a bounded redirect count (`--max-redirs`)
- carry and persist cookies across calls (`-b` / `-c`)
- override the naturalized User-Agent and headers when a specific client shape is needed
- include response headers (`-i`), write the body to a file (`-o`), and report
  outcome fields (`-w`, e.g. `%{http_code}`, `%{url_effective}`)

Common usage:

```bash
curl <url>
curl -X POST -d 'a=1&b=2' <url>
curl -H 'Authorization: Bearer ...' -i <url>
curl -L -b 'sid=abc' -c jar.txt <url>
```

Notes:

- Requests are recorded as HTTP evidence through the runner proxy; this is the
  first-class path for evidence-backed HTTP probing.
- A browser User-Agent and header set are applied only where you did not set them;
  `-A` and `-H` always win.
- Unsupported flags are rejected rather than silently ignored, so behavior is
  never quietly different from what was asked.

## Related concepts

- Use [spray](spray.md) for breadth (many URLs, fingerprints, exposed paths) and
  curl for a single, precise, evidence-backed request.
- Deeper crawling is [katana](katana.md); rendered interaction is
  [playwright](playwright.md).
