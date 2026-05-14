---
name: browser
description: Use this skill to learn how to use the browser pseudo-command for headless browsing, screenshots, and network capture.
internal: true
---

# browser

Headless Chromium browser for interacting with JS-rendered pages, taking screenshots, capturing network traffic, and executing JavaScript. Powered by go-rod with stealth anti-bot-detection enabled by default.

## Subcommands

### navigate
Open a URL, wait for JavaScript to render, return the visible text content. Ideal for extracting readable content from SPAs and JS-heavy pages.
```bash
browser navigate <url> [--timeout <seconds>] [--user-agent <string>]
```

### screenshot
Take a screenshot of a page and save to a PNG file. Returns the file path. Use `--full-page` to capture the entire scrollable page.
```bash
browser screenshot <url> [--output <filename>] [--full-page] [--timeout <seconds>]
```

### content
Extract the full rendered HTML after JavaScript execution. Returns the complete DOM including dynamically generated elements.
```bash
browser content <url> [--timeout <seconds>] [--user-agent <string>]
```

### eval
Execute a JavaScript expression on a page and return the result. The expression is wrapped in an arrow function automatically.
```bash
browser eval <url> <expression>
browser eval <url> --script "document.querySelectorAll('a').length"
browser eval <url> "JSON.stringify(performance.getEntries())"
```

### network
Navigate to a URL and capture all network requests and responses. Extremely useful for API endpoint discovery during reconnaissance.
```bash
browser network <url> [--timeout <seconds>] [--user-agent <string>]
```

Output includes: HTTP method, status code, URL, content-type, and response size for every request.

### pdf
Generate a PDF of the rendered page. Useful for archiving and reporting.
```bash
browser pdf <url> [--output <filename>] [--timeout <seconds>]
```

## Common Options

| Option | Default | Description |
|--------|---------|-------------|
| `--timeout <seconds>` | 30 | Page load and JS execution timeout |
| `--user-agent <string>` | Chrome default | Custom User-Agent header |
| `--output <filename>` | auto-generated | Output file for screenshot/pdf |
| `--full-page` | false | Capture full scrollable page (screenshot only) |
| `--script <js>` | - | JS expression (eval only, alternative to positional arg) |

## Security Scanning Use Cases

- **JS-rendered recon**: Extract content from SPAs that `web_fetch` cannot render
- **API discovery**: Use `network` to capture all XHR/fetch calls a page makes
- **Evidence capture**: Screenshot vulnerable pages for pentest reports
- **Form analysis**: Use `eval` to inspect form fields, CSRF tokens, hidden inputs
- **JS source analysis**: Extract and evaluate client-side JavaScript

## Notes

- The browser launches on first use (lazy init) and is reused across calls.
- Each execution uses a fresh incognito context for session isolation.
- Stealth mode is always enabled to bypass Cloudflare and similar bot detection.
- Screenshots and PDFs are saved relative to the working directory.
- Chromium is automatically downloaded on first launch if not found on the system.
