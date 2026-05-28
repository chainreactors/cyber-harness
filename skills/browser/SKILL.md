---
name: browser
description: Use this skill to learn how to use the browser pseudo-command for headless browsing, screenshots, network capture, and interactive vulnerability verification.
internal: true
---

# browser

Headless Chromium browser for interacting with JS-rendered pages, taking screenshots, capturing network traffic, executing JavaScript, and performing **multi-step interactive vulnerability verification**. Powered by go-rod with stealth anti-bot-detection and katana script injection for smart form discovery and filling.

## Unified URL Or Session Commands

The first argument can be either a URL or an existing session name. If it matches a live session, the command runs against that persistent page; otherwise it opens the URL in a fresh incognito context.

### navigate
Open a URL and return visible text, or extract visible text from the current session page.
```bash
browser navigate <url> [--timeout <seconds>] [--user-agent <string>]
browser navigate <session> [selector]
```

### screenshot
Take a screenshot of a URL or session page. Session mode supports element-level screenshots.
```bash
browser screenshot <url> [--output <filename>] [--full-page] [--timeout <seconds>]
browser screenshot <session> [--output <filename>] [--full-page] [--selector <selector>]
```

### content
Extract rendered HTML from a URL or session page.
```bash
browser content <url> [--timeout <seconds>] [--user-agent <string>]
browser content <session> [selector]
```

### eval
Execute a JavaScript expression on a URL or session page.
```bash
browser eval <url> <expression>
browser eval <url> --script "document.querySelectorAll('a').length"
browser eval <session> "document.title"
```

### network
Navigate to a URL and capture all network requests/responses, or control session network capture.
```bash
browser network <url> [--timeout <seconds>]
browser network <session> --start
browser network <session> --dump
browser network <session> --stop
```

## Stateless-Only Commands

### pdf
Generate a PDF of the rendered page.
```bash
browser pdf <url> [--output <filename>] [--timeout <seconds>]
```

## Session Subcommands (multi-step interactive workflows)

Sessions persist a browser page across multiple tool calls. This enables multi-step vulnerability verification: open a page, discover forms, fill inputs, submit, and check results.

### open / close
```bash
browser open <url> [--session <name>] [--ttl <seconds>] [--timeout <seconds>] [--op-timeout <seconds>]
browser close <session>
```
- Sessions auto-expire after `--ttl` seconds (default: 120s)
- Each session operation is serialized and has an `--op-timeout` deadline (default: 30s)
- Max 8 concurrent sessions
- Session name is auto-generated if `--session` is omitted

### discover
Call katana's injected JS to enumerate all interactive elements on the page: forms (with their fields), buttons, and elements with onclick handlers.
```bash
browser discover <session>
```
Output lists forms with field names, types, and selectors. Selectors may be CSS or `xpath:<xpath>`; interaction commands accept both.

### autofill
Smart form filling using katana's heuristics. Automatically infers values based on input type. Override specific fields with `--data`.
```bash
browser autofill <session> [--form 0] [--data "username=admin,password=test123"]
```

### click / fill / select / wait
```bash
browser click <session> <selector>
browser fill <session> <selector> <value>
browser select <session> <selector> <value>
browser wait <session> <selector|--idle|--stable>
```

### extraction and current URL
```bash
browser navigate <session> [selector]       # Extract visible text
browser content <session> [selector]        # Extract HTML
browser eval <session> <js-expression>      # Execute JS in session
browser screenshot <session> [--output f] [--selector s] [--full-page]
browser url <session>                       # Current URL and title
```
Short aliases are also available for session mode: `text`, `html`, `seval`, `sshot`, and `netcap`.

### dialog (XSS verification)
Capture JavaScript alert/confirm/prompt dialogs. Arm the listener before triggering the payload.
```bash
browser dialog <session> --arm     # Start listening
browser dialog <session> --check   # Return captured dialogs (JSON)
browser dialog <session> --disarm  # Stop listening
```

### cookies
```bash
browser cookies <session> --list
browser cookies <session> --set name=value [name2=value2]
browser cookies <session> --clear
```

## Vulnerability Verification Workflows

### XSS Verification
```bash
browser open http://target.com/search --session xss
browser discover xss
browser dialog xss --arm
browser fill xss "input[name=q]" "<script>alert('xss_canary_8f2a')</script>"
browser click xss "button[type=submit]"
browser wait xss --stable
browser dialog xss --check
# If captured: {"type":"alert","message":"xss_canary_8f2a"} then confirmed
browser screenshot xss --output xss_evidence.png
browser close xss
```

### SQLi via Login Bypass
```bash
browser open http://target.com/login --session sqli
browser autofill sqli --form 0 --data "username=admin' OR '1'='1,password=x"
browser click sqli "button[type=submit]"
browser wait sqli --stable
browser navigate sqli
# Check for dashboard/admin content
browser close sqli
```

### CAPTCHA + Weak Password (with vision tool)
```bash
browser open http://target.com/login --session s1
browser discover s1
browser screenshot s1 --selector "img#captcha" --output captcha.png
# Then use vision tool: vision captcha.png "Return ONLY the CAPTCHA text"
browser autofill s1 --form 0 --data "username=admin,password=admin123,captcha=<solved>"
browser click s1 "button[type=submit]"
browser wait s1 --stable
browser url s1
browser close s1
```

### Auth Bypass via Cookie
```bash
browser open http://target.com/ --session auth
browser cookies auth --set role=admin
browser eval auth "location.href='/admin'"
browser wait auth --stable
browser navigate auth
browser screenshot auth --output auth_evidence.png
browser close auth
```

## Notes

- The browser launches on first use (lazy init) and is reused across calls.
- Stateless commands use fresh incognito contexts. Session commands persist pages.
- Stealth mode is always enabled (go-rod/stealth).
- Katana JS scripts (page-init.js + utils.js) are injected into session pages for form/element discovery.
- Sessions are garbage-collected after TTL expiry.
- Chromium is automatically downloaded on first launch if not found.
