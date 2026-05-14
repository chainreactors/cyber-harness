---
name: web_fetch
description: Use this skill to learn how to use the web_fetch pseudo-command.
internal: true
---

# web_fetch

Fetch a URL and return content as readable text. HTML is auto-converted to Markdown.

```bash
web_fetch <url> [--extract <hint>]
```

- `<url>`: target URL. If no scheme is provided, HTTPS is assumed; explicit HTTP is preserved.
- `--extract <hint>`: optional focus hint to return matching sections when possible.

```bash
web_fetch https://nvd.nist.gov/vuln/detail/CVE-2024-1234
web_fetch https://example.com/advisory --extract "CVSS score"
```
