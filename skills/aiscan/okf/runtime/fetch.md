---
type: Tool Playbook
title: fetch
description: Fetch an HTTP or HTTPS URL and return readable text, with optional focused extraction for advisories, documentation, and vulnerability research.
tags: [runtime, http, research]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# fetch — URL Content Reader

Use `fetch` to retrieve a URL and normalize its response into text suitable for
analysis. The optional extraction hint narrows long pages to the relevant
section.

## Usage

```text
fetch <url> [--extract <hint>]
```

## Related concepts

- Use [playwright](/easm/playwright.md) instead when content requires browser
  rendering or interaction.
- Fetched text can be piped into [proton](/easm/proton.md) for sensitive-data
  inspection or used alongside [cyberhub](search.md) during vulnerability
  research.
