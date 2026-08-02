---
type: Tool Playbook
title: fetch
description: Fetch an HTTP or HTTPS URL and return readable text, with optional focused extraction for advisories, documentation, and vulnerability research.
tags: [runtime, http, research]
status: stable
generated: { by: process:tool-registry-description, at: 2026-08-02T00:00:00Z }
---

# fetch — URL Content Reader

Use `fetch` to retrieve a URL and normalize its response into text suitable for
analysis. The optional extraction hint narrows long pages to the relevant
section.

## Usage

```text
fetch <url> [--extract <hint>]
```
