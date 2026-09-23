# Fuzz

Post-scan parameter review. After scan/spray discovers web endpoints, identify inputs that deserve focused validation without treating every parameter as mandatory work.

## Timing

Use this skill when the user explicitly asks for parameter review, or when deep testing discovers parameterized URLs, forms, or manually observed inputs worth reviewing. In full builds, `katana -u <target> -d 2 -f qurl` can help enumerate URLs with query strings. Spray crawl strips query parameters by design; katana preserves them when available.

This is not a request to fuzz every parameter. Select a small set of high-value candidates from observed evidence and stop when the branch is not producing useful signal.

## Target Selection

Prioritize inputs that are security-relevant: reflected values, parameters that influence backend state, authenticated or privileged actions, file/upload surfaces, dynamic path segments, and unusual API endpoints. Skip static assets, third-party CDNs, and low-value noise.

When several inputs compete for time, prefer authorization-sensitive and backend-shaping parameters first: object IDs, tenant/user/account IDs, export/report filters, `sort`, `order`, `orderBy`, `fields`, `include`, and pagination cursors. Try values learned from one endpoint against related endpoints when the field names or object types line up.

## Standard

- Capture a baseline before drawing conclusions.
- Change one variable at a time when comparing behavior.
- Use unique canaries or measurable signals instead of generic strings.
- Treat a single anomaly as a lead, not a finding.

## Confirmation Standard

A loot is confirmed only when there is a measurable, reproducible difference from baseline and the evidence demonstrates security impact. Without baseline, reproduction, and impact evidence, classify it as potential or unverified.

Apply the `verify` skill's validation rules for all loots.
