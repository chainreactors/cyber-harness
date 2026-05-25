---
name: fuzz
description: Post-scan parameter fuzzing methodology. After scan/spray discovers web endpoints, evaluate and fuzz interesting parameters for injection vulnerabilities.
internal: true
---

# Parameter Fuzzing

## Timing

After `scan` or `spray --crawl`, enumerate parameterized URLs. In full builds, use `katana -u <target> -d 2 -f qurl`; otherwise work from scan/spray URLs, forms, and manually observed parameters. Spray crawl strips query parameters by design; katana preserves them when available.

## Target Selection

Fuzz candidates: query parameters, dynamic path segments, POST bodies from form actions, headers that influence output (Host, X-Forwarded-For). Prioritize parameters that reflect in responses or interact with backend state. Skip static assets and third-party CDNs.

## Methodology

For each candidate parameter:

1. **Baseline first** — capture the normal response (status, body length, timing) before injecting anything.
2. **Classify the interaction** — reflection in output → test injection into that output context. Backend data influence → test data-layer injection. Opaque output → try timing-based blind techniques. Verbose errors → use error-based confirmation.
3. **One variable at a time** — hold everything else constant.
4. **Use unique canaries** — generate a random marker per test (e.g., `aiscan_7f3b2c9`). Never rely on generic strings (`alert(1)`, `<script>`) that may exist naturally in the page.
5. **Reproduce before reporting** — a single anomalous response is a lead. Confirm with a distinct payload.
6. **Respect defenses** — if WAF blocks, vary approach or slow down. Don't retry identical payloads.

## Confirmation Standard

A finding is confirmed only when there is a measurable, reproducible difference between baseline and injected responses, proven by: the exact payload (curl-reproducible), the response fragment showing exploitation, and the baseline for comparison. Without all three, classify as "potential/unverified".

Apply the `verify` skill's validation rules for all findings.
