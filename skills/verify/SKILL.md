---
name: verify
description: Use this skill to validate scan findings by actively probing targets and reasoning about evidence quality. Determines if a vulnerability is genuinely confirmed.
agent: true
agent_max_turns: 10
agent_background: true
---

# Verify

Verify is aiscan's active finding validation skill. Scanner output is a **lead**, not proof. This skill probes the target to determine whether the evidence actually supports a confirmed security issue.

## Core Rule

NEVER report a vulnerability as "confirmed" based solely on scanner tool output. You MUST actively probe the target.

## Active Verification Protocol

You have access to bash tools. Use them to verify findings against live targets.

### Step 1: Reachability Check

Before anything else, confirm the target is reachable:
- TCP: `nc -zw3 <host> <port>` or `timeout 5 bash -c 'echo > /dev/tcp/<host>/<port>'`
- HTTP: `curl -sk --connect-timeout 5 -o /dev/null -w '%{http_code}' <url>`

If the target is unreachable, return `not_confirmed` with evidence "target unreachable - port closed or filtered".

### Step 2: Service Identity

Confirm the actual service matches the scanner's claim:
- Banner grab: `echo | nc -w3 <host> <port>` or `curl -sI <url>`
- Compare response against the claimed service (e.g., scanner says "Nacos" but response is Spring Boot)

If service identity doesn't match, return `not_confirmed`.

### Step 3: Claim-Specific Tests

Test the specific vulnerability claim:

| Claim Type | How to Verify |
|-----------|---------------|
| Unauthorized access (Redis/MongoDB/etc.) | Connect and check for auth requirement: `redis-cli -h <host> -p <port> ping` — look for `-NOAUTH` vs `PONG` |
| Default credentials | Test claimed credentials against actual login |
| Information disclosure | Fetch the specific endpoint, confirm sensitive data is present |
| Known CVE | Check version string against the affected range and attempt a safe PoC if possible |
| Web vulnerability (XSS/SQLi) | Send unique canary, compare against baseline |
| Open management console | Fetch URL, confirm it returns admin/management interface content |
| **XSS (reflected/stored)** | Use browser session: `open` → `discover` → `dialog --arm` → `fill` payload → `click` submit → `dialog --check` for alert |
| **SQLi via login** | Use browser session: `open` → `autofill --data "username=admin' OR 1=1--"` → `click` submit → check `url`/`navigate` for admin content |
| **Weak creds + CAPTCHA** | Use browser session: `open` → `discover` → `screenshot --selector` captcha → `vision` to solve → `autofill --data` with creds + captcha → `click` submit |
| **Auth bypass via cookies** | Use browser session: `open` → `cookies --set role=admin` → `eval` navigate to admin → check `navigate` text |

### Tool Selection Decision Tree

```
Is the target a simple HTTP endpoint?
├── YES → use curl/nc (faster, lighter)
└── NO (JS-rendered, SPA, form submission needed)
    ├── Just need rendered content? → browser navigate/content
    └── Need multi-step interaction?
        └── browser open → discover → fill/autofill → click → check results
            ├── Page has CAPTCHA? → browser screenshot --selector + vision tool
            ├── Need XSS dialog detection? → browser dialog --arm before payload
            └── Need to track requests? → browser network --start before action
```

### Step 4: Baseline Comparison

When verifying web findings:
- Compare suspicious endpoint response against a normal endpoint on the same host
- For injection claims: compare response with payload vs response with benign input

## Engine-Specific Interpretation

- **neutron** template match = potential lead requiring independent verification. "no templates selected" = nothing matched, not a finding.
- **zombie** HTTP 200 = check response BODY for authenticated content. A login page returns 200 normally — that is NOT a successful login.
- **spray** fingerprint = informational asset intelligence, not a vulnerability.
- **gogo** port open = service exposure, confirm what's actually running.
- **POC/exploit** output can be confirmed only when the evidence shows successful exploitation, not just that a template matched.
- **weak credential** output can be confirmed only when evidence shows valid authentication or authenticated content.

## Verifying Injection Findings

When verifying XSS/SQLi/injection-type scanner output:

- Grep for a unique random canary string (e.g. `aiscan_xss_a7f3b2`), not generic payloads like `alert(1)`.
- Compare the injected response against a baseline (same endpoint, normal parameter value).
- A finding requires a measurable difference.

## Status Determination

- **confirmed**: evidence directly supports the security risk with reproducible proof from your active probing
- **info**: finding is real but informational (fingerprint, version disclosure, non-exploitable exposure)
- **not_confirmed**: probing completed but did not support the claim; use this for target unreachable, service mismatch, auth required, 401/403/WAF denial without protected data, unaffected version, or insufficient evidence
- **inconclusive**: rare; probing could not be completed or evaluated because of tool failure, unstable connectivity, or contradictory responses

## Output Format

Return a single JSON object:

```json
{"status":"confirmed|info|not_confirmed|inconclusive","target":"<host:port or URL>","summary":"<one sentence>","detail":"<exact command output as evidence>","remediation":"<fix advice or empty>"}
```

Only output the JSON object. Do not add markdown fences or extra text.

Include the **exact command output** in the `detail` field as evidence. This allows humans to independently verify your conclusion.
