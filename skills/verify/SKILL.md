---
name: verify
description: Use this skill to validate scan findings by reasoning about evidence quality and determining if a vulnerability is genuinely confirmed.
agent: true
agent_max_turns: 10
agent_background: true
---

# Verify

Verify is aiscan's finding validation skill. Scanner output is a **lead**, not proof. This skill determines whether the evidence actually supports a confirmed security issue.

## Core Rule

NEVER report a vulnerability as "confirmed" based solely on scanner tool output.

## Engine-Specific Interpretation

- **neutron** template match = potential lead requiring independent verification. "no templates selected" = nothing matched, not a finding.
- **zombie** HTTP 200 = check response BODY for authenticated content. A login page returns 200 normally — that is NOT a successful login.
- **spray** fingerprint = informational asset intelligence, not a vulnerability.
- **POC/exploit** output can be confirmed only when the evidence shows successful exploitation of the target, not just that a template or signature matched.
- **weak credential** output can be confirmed only when the evidence shows valid authentication or authenticated content.

## Verifying Injection Findings

When verifying XSS/SQLi/injection-type scanner output:

- Grep for a unique random canary string (e.g. `aiscan_xss_a7f3b2`), not generic payloads like `alert(1)` — the page itself may contain these.
- Compare the injected response against a baseline (same endpoint, normal parameter value). A finding requires a measurable difference.

## Confirmation Requirements

Every confirmed finding MUST include:
1. Exact curl-reproducible payload
2. Response evidence
3. Baseline comparison

If you cannot independently verify with unique evidence, do not use `confirmed`. For JSON skill output, use `not_confirmed` when evidence is insufficient, ambiguous, or contradicted by baseline; use `inconclusive` when the available data cannot be evaluated. In human-facing reports, describe the issue as "potential/unverified" and include the raw tool output.

## Status Determination

- **confirmed**: evidence directly supports the security risk with reproducible proof
- **info**: finding is real but informational (fingerprint, version disclosure, non-exploitable)
- **not_confirmed**: evidence is insufficient, ambiguous, or contradicted by baseline
- **inconclusive**: cannot determine either way

## Assessment Criteria

- Does the evidence include a specific CVE or vulnerability identifier?
- Is there proof of successful exploitation (not just detection)?
- Is the finding severity consistent with the evidence?
- Could this be a false positive from generic signature matching?
