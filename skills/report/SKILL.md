---
name: report
description: Use this skill to generate a concise security scan report from scan results in natural language.
---

# Report

Generate a concise security scan report from the provided scan results.

## Verification Semantics

The scan input uses markdown annotations to convey verification status. Treat these as authoritative:

| Annotation | Meaning | Action |
|-----------|---------|--------|
| `**[verified]** ...` | Active probing confirmed the finding | Critical Findings |
| `~~...~~ *(not confirmed)*` | Active probing did not support the claim | Dismissed Findings only |
| `**[inconclusive]** ...` or `[ai:inconclusive]` | Verification could not reach a conclusion | Potential Risks only |
| `[sniper]` / `[ai:info]` | CVE intelligence from fingerprints, not proof | Potential Risks or Informational only |
| `[fingerprint]` | Technology identification | Services & Fingerprints only |
| Unannotated `[vuln]` / `[risk]` | Scanner lead without active verification | Include only with "unverified scanner match" caveat |

### Non-Negotiable Rules

- Fingerprint != vulnerability. Detecting Shiro, Nacos, Druid, etc. means technology is present, not exploitable.
- Sniper CVE intelligence is a lead. Never report it as a confirmed exploit.
- Strikethrough/not_confirmed findings are excluded from Critical Findings under all circumstances.
- Separate confirmed vulnerabilities from unverified leads in the summary.

## Report Format

Use this exact structure:

```
## Summary

One paragraph overview: what was scanned, key stats (targets, services, vulns found), overall risk assessment.
Count confirmed vulnerabilities separately from unverified leads. Strikethrough findings are not vulnerabilities.

## Critical Findings

List verified findings first. Unannotated scanner matches may appear only with "unverified scanner match" stated clearly.
For each:
- **[target]** — vulnerability description, CVE if applicable, impact, verification status

## Potential Risks (Unverified)

Sniper intelligence, inconclusive verification, or scanner leads that lack active confirmation.
- **[target]** — what was detected, why it warrants investigation, manual verification step

## Services & Fingerprints

Brief list of discovered services and notable fingerprints (focus on security-relevant ones).

## Weak Credentials

List any discovered weak passwords/credentials. Note verification status.

## Dismissed Findings

Findings that were actively verified and determined to be false positives (strikethrough items).
Brief list so the reader knows what was checked and cleared.

## Recommendations

3-5 prioritized remediation actions based on confirmed findings.
```

## Rules

- Be concise. Each section should be 2-5 lines max.
- Only include sections that have relevant content.
- Do not invent findings not present in the scan data.
- Prioritize by severity: critical > high > medium.
- Use plain markdown, no code fences around the report.
- If no significant findings after applying verification filters, say so clearly. An honest "no confirmed vulnerabilities" is far more valuable than inflated severity.
