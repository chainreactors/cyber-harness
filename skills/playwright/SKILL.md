---
name: playwright
description: Use this skill to learn how to use the playwright pseudo-command for headless browsing, screenshots, network capture, and interactive vulnerability verification. Aligned with microsoft/playwright-cli conventions.
internal: true
---

# playwright

Headless Chromium browser for interacting with JS-rendered pages, taking screenshots, capturing network traffic, executing JavaScript, and performing **multi-step interactive vulnerability verification**. Powered by go-rod with stealth anti-bot-detection and katana script injection for smart form discovery, event listener capture, and SPA route detection.

Command names are aligned with [microsoft/playwright-cli](https://github.com/microsoft/playwright-cli) for familiarity.

## Global Session Flag

Use `-s=<name>` or `-s <name>` on any command to target a named session, matching the playwright-cli convention:
```bash
playwright -s=mySession click "button"       # equivalent to: playwright click mySession "button"
playwright -s=s1 goto                        # extract text from session s1
```

Environment variable: `PLAYWRIGHT_CLI_SESSION=<name>` sets the default session when `-s` is not provided.

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

### eval
Execute a JavaScript expression on a URL or session page.
```bash
playwright eval <url> <expression>
playwright eval <url> --script "document.querySelectorAll('a').length"
playwright eval <session> "document.title"
```
Alias: `evaluate`

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

### open / close / sessions / attach / detach
```bash
playwright open <url> [--session <name>] [--timeout <seconds>] [--op-timeout <seconds>] [--no-speed-up]
                      [--headed]              # Launch with GUI (non-headless)
                      [--cdp <ws-url>]         # Connect to external browser via CDP endpoint
playwright close <session>                    # Close a session (release resources)
playwright sessions                           # List all active sessions
playwright list                               # Alias for sessions
playwright close-all                          # Close all sessions
playwright kill-all                           # Kill browser process and all sessions
playwright delete-data <session>              # Close session and discard all data
playwright attach --cdp <url> [--session name]  # Attach to running browser via CDP
playwright detach <session>                   # Disconnect from attached session (browser keeps running)
```
- Sessions persist until explicitly closed via `playwright close <session>` or process exit
- `--no-speed-up` disables setTimeout/setInterval acceleration (use for timing-sensitive verification)
- Each session operation is serialized and has an `--op-timeout` deadline (default: 30s)
- Max 8 concurrent sessions
- Session name is auto-generated if `--session` is omitted
- `playwright sessions` / `playwright list` lists all active sessions with URL and age
- Console messages are automatically captured from session open (retrieve with `console`)
- `attach` connects to an external browser and imports its existing tabs; use `detach` to disconnect without closing
- `close` on an attached session disconnects without killing the external browser

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

### click / fill / type / select-option / wait-for
```bash
playwright click <session> <selector>
playwright dblclick <session> <selector>
playwright fill <session> <selector> <value>
playwright type <session> <selector> <text>
playwright press <session> <selector> <key>
playwright hover <session> <selector>
playwright select-option <session> <selector> <value>
playwright check <session> <selector>
playwright uncheck <session> <selector>
playwright set-input-files <session> <selector> <path...>
playwright upload <session> <selector> <path...>        # alias for set-input-files
playwright focus <session> <selector>
playwright blur <session> <selector>
playwright tap <session> <selector>
playwright wait-for <session> <selector|--idle|--stable>
playwright wait-for-url <session> <url-substring>
playwright wait-for-request <session> <url-substring>
playwright wait-for-response <session> <url-substring>
playwright dispatch-event <session> <selector> <event-type>
```

### Extraction and current URL
```bash
playwright goto <session> [selector]           # Extract visible text
playwright content <session> [selector]        # Extract HTML
playwright eval <session> <js-expression>      # Execute JS in session
playwright screenshot <session> [--output f] [--selector s] [--full-page]
playwright url <session>                       # Current URL and title
playwright get-attribute <session> <sel> <attr>
playwright input-value <session> <selector>
playwright inner-text <session> <selector>
playwright is-visible <session> <selector>
playwright is-hidden <session> <selector>
playwright is-checked <session> <selector>
playwright is-disabled <session> <selector>
playwright is-enabled <session> <selector>
```

Short aliases (backward compat): `text-content`, `inner-html`, `navigate`, `evaluate`, `select`, `wait`, `text`, `html`, `seval`, `sshot`.

### Tab Management (playwright-cli aligned)
Manage multiple tabs within a single session. Each session starts with one tab; new tabs share the same browser context (cookies, storage).
```bash
playwright tab-list <session>                 # List all tabs (* marks active)
playwright tab-new <session> [url]            # Open a new tab, optionally navigate
playwright tab-close <session> [index]        # Close a tab (default: active tab)
playwright tab-select <session> <index>       # Switch the active tab by index
```
- `tab-new` injects stealth + console hooks (same as `open`)
- All session commands (click, fill, eval, etc.) operate on the **active tab**
- Use `tab-select` to switch focus before interacting with a different tab

### Navigation
```bash
playwright reload <session>
playwright go-back <session>
playwright go-forward <session>
```

### dialog (XSS verification)
Capture JavaScript alert/confirm/prompt dialogs.
```bash
playwright dialog-accept <session> [prompt-text]  # Accept the next dialog (one-shot)
playwright dialog-dismiss <session>               # Dismiss the next dialog (one-shot)
playwright dialog <session> --arm                 # Start listening (captures all dialogs)
playwright dialog <session> --check               # Return captured dialogs (JSON)
playwright dialog <session> --disarm              # Stop listening
```

### Storage Management (playwright-cli aligned)

#### Cookies
Individual commands aligned with microsoft/playwright-cli `cookie-*` style:
```bash
playwright cookie-list <session>                     # List all cookies
playwright cookie-get <session> <name>               # Get a specific cookie by name
playwright cookie-set <session> <name=value> [...]   # Set one or more cookies
playwright cookie-delete <session> <name>            # Delete a specific cookie
playwright cookie-clear <session>                    # Clear all cookies
```
Legacy alias: `cookies <session> --list|--set k=v|--clear`

#### localStorage
```bash
playwright localstorage-list <session>               # List all localStorage items
playwright localstorage-get <session> <key>          # Get a localStorage item
playwright localstorage-set <session> <key> <value>  # Set a localStorage item
playwright localstorage-delete <session> <key>       # Delete a localStorage item
playwright localstorage-clear <session>              # Clear all localStorage
```

#### sessionStorage
```bash
playwright sessionstorage-list <session>               # List all sessionStorage items
playwright sessionstorage-get <session> <key>          # Get a sessionStorage item
playwright sessionstorage-set <session> <key> <value>  # Set a sessionStorage item
playwright sessionstorage-delete <session> <key>       # Delete a sessionStorage item
playwright sessionstorage-clear <session>              # Clear all sessionStorage
```

#### State
Save and load the full browser state (cookies + localStorage) in Playwright-compatible JSON format.
```bash
playwright state-save <session> <file>               # Save cookies + localStorage to file
playwright state-load <session> <file>               # Load cookies + localStorage from file
```
Also available as flags: `open --save-storage <file>` / `open --load-storage <file>` / `close --save-storage <file>`.

### DevTools

#### console
Console messages (console.log, console.error, etc.) are automatically captured from session open.
```bash
playwright console <session>           # Show all captured console messages
playwright console <session> --clear   # Clear captured messages
```

#### snapshot
Capture the page's accessibility tree. More compact than raw HTML — ideal for understanding page structure.
```bash
playwright snapshot <session>                # Full accessibility tree
playwright snapshot <session> --depth 3      # Limit tree depth
```

#### requests / request
Network requests are auto-captured from session open. List all or inspect a specific request.
```bash
playwright requests <session>                # List all captured requests (index, method, status, URL)
playwright request <session> <index>         # Show full detail (headers, post data, response headers)
```

#### route-list
List active request interception rules set via `route`.
```bash
playwright route-list <session>              # List all active routes
```

### Headers & Interception
```bash
playwright set-extra-headers <session> <json>          # Add extra HTTP headers (e.g. Authorization)
playwright set-viewport <session> <width> <height>     # Set viewport dimensions
playwright route <session> <pattern> --fulfill|--abort|--continue [options]
playwright route-list <session>                        # List active routes
playwright unroute <session>                           # Remove all request interception routes
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

When `--record` is active, every interaction command (click, fill, press, select-option, wait-for, eval, etc.) is automatically captured as a nuclei headless action.

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
# 1. Record a reproducible browser interaction
playwright open http://target.com/search --session s1 --record
playwright fill s1 "input[name=q]" "aiscan_canary_8f2a"
playwright click s1 "button[type=submit]"
playwright wait-for s1 --stable
playwright text-content s1
playwright record s1 --save interaction.yaml --id browser-interaction
playwright close s1

# 2. Replay against other targets
playwright template interaction.yaml http://target2.com/search
playwright template interaction.yaml http://target3.com/search
```

### What gets recorded

| playwright command | nuclei headless action |
|---|---|
| `open --record` (initial) | `navigate` with `{{BaseURL}}` |
| `click` | `click` |
| `fill` / `type` | `text` |
| `press` | `keyboard` |
| `select-option` | `select` |
| `eval` | `script` |
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
# Run a recorded template
playwright template recorded-flow.yaml http://target.com

# Run with payload overrides
playwright template recorded-flow.yaml http://target.com --payload query=test

# Run a self-contained template (URL embedded in template)
playwright template self-contained-check.yaml http://target.com
```

## Security Use Notes

Use browser automation when evidence depends on rendered DOM, user interaction, dialogs, cookies, storage, client-side routing, screenshots, or network traces. Keep tests low-risk, use unique canaries for input experiments, and close sessions when finished. When an interaction is confirmed as useful evidence, save a screenshot or recorded template only as needed for reproducibility.

## playwright-cli Command Mapping

| microsoft/playwright-cli | aiscan playwright | notes |
|---|---|---|
| `open` | `open` | |
| `goto` | `goto` | |
| `close` | `close` | |
| `click` | `click` | |
| `dblclick` | `dblclick` | |
| `fill` | `fill` | |
| `type` | `type` | |
| `press` | `press` | |
| `hover` | `hover` | |
| `select` | `select-option` | alias: `select` |
| `check` | `check` | |
| `uncheck` | `uncheck` | |
| `eval` | `eval` | alias: `evaluate` |
| `screenshot` | `screenshot` | |
| `pdf` | `pdf` | |
| `reload` | `reload` | |
| `go-back` | `go-back` | alias: `back` |
| `go-forward` | `go-forward` | alias: `forward` |
| `cookie-list` | `cookie-list` | |
| `cookie-get` | `cookie-get` | |
| `cookie-set` | `cookie-set` | |
| `cookie-delete` | `cookie-delete` | |
| `cookie-clear` | `cookie-clear` | |
| `localstorage-list` | `localstorage-list` | |
| `localstorage-get` | `localstorage-get` | |
| `localstorage-set` | `localstorage-set` | |
| `localstorage-delete` | `localstorage-delete` | |
| `localstorage-clear` | `localstorage-clear` | |
| `sessionstorage-list` | `sessionstorage-list` | |
| `sessionstorage-get` | `sessionstorage-get` | |
| `sessionstorage-set` | `sessionstorage-set` | |
| `sessionstorage-delete` | `sessionstorage-delete` | |
| `sessionstorage-clear` | `sessionstorage-clear` | |
| `console` | `console` | auto-captured from session open |
| `route` | `route` | |
| `unroute` | `unroute` | |
| `snapshot` | `snapshot` | CDP Accessibility tree |
| `requests` | `requests` | auto-captured from session open |
| `request` | `request` | show detail with headers/body |
| `route-list` | `route-list` | |
| `dialog-accept` | `dialog-accept` | one-shot accept |
| `dialog-dismiss` | `dialog-dismiss` | one-shot dismiss |
| `state-save` / `state-load` | `state-save` / `state-load` | also `--save-storage`/`--load-storage` flags |
| `resize` | `set-viewport` | |
| `tab-list` | `tab-list` | |
| `tab-new` | `tab-new` | |
| `tab-close` | `tab-close` | |
| `tab-select` | `tab-select` | |
| `attach --cdp=<url>` | `attach --cdp <url>` | |
| `detach` | `detach` | |
| `list` | `list` / `sessions` | |
| `close-all` | `close-all` | |
| `kill-all` | `kill-all` | |
| `delete-data` | `delete-data` | |
| `upload` | `upload` | alias: `set-input-files` |
| `-s=<name>` (global flag) | `-s=<name>` / `-s <name>` | also `PLAYWRIGHT_CLI_SESSION` env |
| `open` (headed) | `open --headed` | launch with GUI |

## aiscan Extensions (no playwright-cli equivalent)

| command | purpose |
|---|---|
| `discover` | katana JS hooks — enumerate forms, buttons, event listeners, SPA routes, fetch/WebSocket URLs |
| `autofill` | katana heuristics — smart form filling with auto-inferred values |
| `record` | codegen — record session interactions as nuclei headless YAML (like Playwright's `codegen` but outputs YAML) |
| `template` | run a nuclei-compatible headless YAML template against a target |

## Notes

- The browser launches on first use (lazy init) and is reused across calls.
- Use `--headed` on `open` to launch with a GUI window; use `--cdp <ws-url>` to connect to an existing browser (e.g. Chrome DevTools).
- Stateless commands use fresh incognito contexts. Session commands persist pages.
- Network requests are auto-captured from session open — use `requests`/`request` to inspect.
- Stealth mode is always enabled (go-rod/stealth).
- Katana JS scripts (page-init.js + utils.js) are injected into session pages with **hooks activated** for:
  - Event listener capture (`window.__eventListeners`)
  - SPA route detection via pushState/replaceState/fetch/WebSocket hooks (`window.__navigatedLinks`)
  - Form reset prevention
  - setTimeout/setInterval acceleration (0.1x factor, disable with `--no-speed-up`)
- Console messages are auto-captured from session open — retrieve with `console <session>`.
- Sessions persist until explicitly closed — the agent is responsible for calling `playwright close`.
- Chromium is automatically downloaded on first launch if not found.
- Selectors may be CSS or `xpath:<xpath>` — interaction commands accept both.
