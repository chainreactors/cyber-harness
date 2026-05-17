---
name: spray
description: Use this skill when working with spray for web probing, HTTP fingerprints, exposed paths, and application analysis.
internal: true
---

# Spray

Spray is the web probing and HTTP fingerprint tool in aiscan.

Capabilities:

- probe URLs and HTTP services discovered from targets or service scan output
- collect status code, title, content length, redirects, headers, and response metadata
- match web fingerprints, components, CMS, frameworks, and focus technologies
- discover common files, interesting paths, crawl output, and exposed resources
- report errors, timeouts, blocked responses, and missing evidence

Common usage:

```bash
spray -u <url>
spray -u <url> --finger
spray -l <url-file> --finger
```

Notes:

- Use spray when the task is about web identity, exposed resources, or HTTP evidence.
- Fingerprints and paths describe observed web behavior; user intent decides whether to summarize, analyze, review, or plan follow-up checks.
