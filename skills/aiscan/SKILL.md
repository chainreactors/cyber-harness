---
name: aiscan
description: Use this skill for AIScan's attack surface management and penetration-testing capabilities, including scanner pseudo-commands, supporting security tools, vulnerability verification, evidence handling, and assessment reporting.
---

# AIScan ASM and Penetration Testing

This is the Cyber Harness's built-in attack surface management (ASM) and penetration-testing skill. Use the chainreactors scanner toolkit and supporting tools to discover, analyze, and validate vulnerabilities across authorized target assets. This skill does not define the harness's global identity and must not redirect tasks outside its scope into scanning unless their objective requires it.

## General Execution Tools

Use these capabilities to inspect inputs, execute supporting analysis, and collect evidence:

- `read` / `write` / `glob`: workspace file operations. `read` also loads embedded skill files via `aiscan://` URIs.
- `bash`: run shell commands and pseudo-commands (see below).
- `web_search`: search the web for CVEs, advisories, exploits, and documentation.
- `fetch`: fetch and read a specific URL.
- `record` (optional Windows/Linux SDK builds): capture desktop or visible application-window screenshots and H.264 recordings. It accepts HWND/X11 Window IDs or resolves a PID to its main visible window.

## ASM and Penetration Tools

The pseudo-commands and utilities below provide AIScan's dedicated ASM and penetration-testing toolset. They run through `bash` and are available only when exposed by the current runtime.

### User Tool Restrictions

Treat a user restriction as a constraint on tools and traffic, not as permission to reduce the requested assessment depth. Follow explicit scope and rate limits exactly.

When the user says not to use scanners or automated scanning:

- Do not invoke `scan`, `gogo`, `spray`, `zombie`, `neutron`, `proton`, `passive`, or `katana` unless the user later allows it.
- Use only tools and traffic patterns that remain within the stated restriction. Keep requests targeted and do not expand to related hosts without permission.
- Explain any material coverage gap caused by the restriction in the final result.

### Scanner Pseudo-Commands

All pseudo-commands run through `bash`. They are **not** system binaries.

### Scanners (all builds)

- `scan`: multi-stage pipeline — discovery → web probe → weakpass → POC → verification.
- `gogo`: host, port, service, and banner discovery.
- `spray`: web probing, fingerprints, common files, and crawl.
- `zombie`: weak credential checks for supported services.
- `neutron`: template-based POC execution.
- `proton`: sensitive information scanning — API keys, tokens, credentials, secrets in files or piped data.

Each scanner's detailed flags live in an OKF-style tool concept under `aiscan://skills/aiscan/okf/easm/<command>.md`, loaded automatically on invocation.

aiscan organizes externally produced markdown (tool docs, reports, findings) by referencing mechanisms from [OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md): concept files with YAML frontmatter (`type`, `title`, `tags`, `status`, `verified`, `sources`), per-bundle `index.md` listings, and bundle-relative links. It borrows the mechanism only — full OKF spec compliance is not required.

### Scanners (full-build only)

Available only when they appear in the runtime pseudo-command list:

- `passive`: domain/ICP seed → IPs, CIDRs, domains via cyberspace search (FOFA/Hunter/Shodan/etc.)
- `katana`: deep web crawling with full parameter discovery
- `playwright`: headless Chromium browser for JS-rendered pages, screenshots, network capture, and interactive verification. Reference: `aiscan://skills/aiscan/okf/easm/playwright.md`. Key commands: `playwright goto <url>`, `playwright screenshot <url>`, `playwright open <url> --session s1`, `playwright discover s1`, `playwright close s1`.

### Utilities

- `arsenal`: security tool package manager (22+ tools from chainreactors & projectdiscovery). Run `arsenal list` first. Reference: `aiscan://skills/aiscan/okf/runtime/arsenal.md`.
- `cyberhub`: search fingerprints and POC templates. Key: `cyberhub search --finger <name>`. Reference: `aiscan://skills/aiscan/okf/runtime/search.md`.
- `tmux`: session management. Key: `tmux ls`, `tmux capture-pane -t <id>`, `tmux kill-session -t <id>`. Reference: `aiscan://skills/aiscan/okf/runtime/tmux.md`.
- `proxy`: proxy nodes and proxied execution. Key: `proxy <url> <cmd>`, `proxy auto <sub-url>`. Reference: `aiscan://skills/aiscan/okf/runtime/proxy.md`.
- `ioa`: multi-agent collaboration via shared message spaces — `ioa space <name> <desc>`, `ioa send`, `ioa read --all`, and `ioa send checkpoint`. Reference: `aiscan://skills/ioa/SKILL.md`; wire-protocol formats live in the ioa module skills (`ioa://skills/<checkpoint|handoff|swarm|team>/SKILL.md`). Publish vulnerability discoveries per `aiscan://skills/aiscan/okf/runtime/ioa-finding.md`.

## Scan Output Consumption

- Inline output: consume directly when the scan returns quickly.
- Session id: use `tmux capture-pane -t <id>` to read. See tmux reference.
- Use `-j` for machine-readable JSON Lines output. Do not assume a result file exists unless you passed an output flag.

## Report Generation

When producing a scan report, follow the format and verification semantics in `aiscan://skills/aiscan/reference/report.md`. Key rules: separate confirmed findings from unverified leads, require executable PoC for confirmed status, do not inflate severity.

## Execution Environment

`bash` accepts `command`, `wait`, and `timeout`. Every command runs in a tmux session. Pseudo-commands run in-process; others run as shell commands in a PTY. Keep invocations self-contained — no shell state carryover.

- `wait: 0` (default): stay in the foreground until completion.
- `wait: N`: move a still-running command to background after N seconds and return its session id. This is not a failure or cancellation.
- omitted `timeout`: use the 600s safety timeout. `timeout: N` cancels the command after N total seconds, including background time. `timeout: 0` disables the command timeout.

Background completion is delivered through the inbox automatically. Incremental output is best-effort; completion delivery is retained with higher priority.

Interactive shells (`su`, `python`, `mysql` prompts) do not work. Use "one command in → stdout out" pattern.

### Pipes and Redirections

Both shell commands and pseudo-commands support **single pipes** (`|`). The pipe runs the pseudo-command in-process, captures its output, then feeds it as stdin to the shell pipeline via `sh -c`:
```bash
# pseudo-command piped to shell filters — works natively
proton -i . -c keys | grep critical
scan -i target -j | head -20
spray -u http://target | grep -E "200|301" | wc -l
gogo -i 192.168.1.0/24 | awk '{print $1}' | sort | uniq -c | sort -rn

# shell-only pipes — also work (PTY path)
cat targets.txt | grep -v "#" | sort -u
curl -s http://target/api | jq .
```

Redirections (`>`, `>>`), logical OR (`||`), and command chaining (`&&`, `;`) are **not supported** for pseudo-commands and return an error. Use scanner-native flags for output files (`-f`, `-s`) and filtering (`--severity`, `-o json`).

## Verification Standard

Scanner output is a lead, not a finding. Confirmed status requires:
- Independent, reproducible evidence (curl command, browser replay, or equivalent PoC)
- Demonstrated security impact, not just a status code, banner, or template match
- Baseline comparison for behavioral-difference claims
- 3-5 identifiers tested for authorization/IDOR claims

Non-findings without impact chain: fingerprints, CORS/security headers, GraphQL introspection, open redirect, self-XSS, version disclosure.

## Evidence & Findings

- Collect minimum evidence. Prefer excerpts, hashes, counts over bulk data.
- Keep a progressive findings log at {{findings_path}} for long assessments.
- Suppress standalone P3/low/informational unless user requested inventory or it chains into impact.

## Tool Invocation Rules

1. Keep top-level aiscan flags separate from scanner flags (`aiscan -p` is the prompt; scanner `-p` keeps its native meaning).
2. Prefer pseudo-commands over raw binaries — output is captured and bounded.
3. Non-interactive output only. No progress bars or unbounded streaming.
4. Conservative threads/timeouts for localhost or fragile services.
5. Use `scan --verify=high` when the user asks to validate risky findings.
6. Call `finish` exactly once when the task is complete and all subagents have reported. Do not call it while subagents are running.
