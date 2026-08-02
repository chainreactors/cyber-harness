
# Report

Generate a concise security scan report from the provided scan results.

## Report Structure (OKF-style bundle)

Write the report as a directory of markdown files, borrowing mechanisms from OKF (concept files with YAML frontmatter, an index listing, provenance fields). This references OKF's mechanisms to organize output markdown; full OKF spec compliance is not required.

```
<report-dir>/
  index.md            # human-readable summary (the six sections below)
  findings/
    <finding-id>.md   # one concept file per confirmed finding or noteworthy lead
```

Each `findings/<id>.md` carries frontmatter:

```yaml
---
type: Vulnerability Finding
title: Shiro rememberMe deserialization RCE
severity: critical
status: confirmed            # confirmed | unverified | dismissed
verified:
  - actor: aiscan/<version>  # or human:<id> for human review
    at: 2026-08-02
sources:
  - resource: mitm://flows/17        # captured verification traffic
    title: shiro-key-bounce request/response
  - resource: findings/poc/shiro-rce.yaml   # nuclei template PoC
    title: executed via neutron -u <target> -t findings/poc/shiro-rce.yaml
tags: [rce, shiro]
---
```

Body: target, description, impact, and the exact reproduction steps.

**Evidence sources for confirmed findings** — recommended, exceptions allowed (see verify.md): cite the `mitm`-captured traffic record and the nuclei template PoC that was actually executed to reproduce the issue. When an exception applies (non-HTTP protocol, interactive-only flow, fragile target), state the evidence form used and the reason in the finding body.

## Verification Semantics

The scan input uses markdown annotations to convey verification status. Treat these as authoritative:

| Annotation | Meaning | Action |
|-----------|---------|--------|
| `**[verified]** ...` | Active probing confirmed the loot | Critical Loots + findings/ concept |
| `~~...~~ *(not confirmed)*` | Active probing did not support the claim | Dismissed Leads only |
| `**[inconclusive]** ...` or `[ai:inconclusive]` | Verification could not reach a conclusion | Potential Risks only |
| `[sniper]` / `[ai:info]` | CVE intelligence from fingerprints, not proof | Potential Risks or Informational only |
| `[fingerprint]` | Technology identification | Services & Fingerprints only |
| Unannotated `[vuln]` / `[risk]` | Scanner lead without active verification | Include only with "unverified scanner match" caveat |

### Non-Negotiable Rules

- Fingerprint != vulnerability. Detecting Shiro, Nacos, Druid, etc. means technology is present, not exploitable.
- Sniper CVE intelligence is a lead. Never report it as a confirmed exploit.
- Strikethrough/not_confirmed loots are excluded from Critical Loots under all circumstances.
- Separate confirmed vulnerabilities from unverified leads in the summary.
- No executable PoC means no confirmed finding. Each confirmed item must include a curl/protocol command, saved browser replay, executed nuclei template, or equivalent reproducible artifact.
- Do not write standalone P3/low/informational findings unless the user explicitly requested an inventory or the issue chains into demonstrated impact.
- CORS, security headers, version disclosure, GraphQL introspection, open redirect, and self-XSS stay out of confirmed findings unless the report includes the impact chain evidence.
- If all material is below P3/medium or lacks executable reproduction, say "no confirmed reportable vulnerability" instead of inflating severity.
- Keep the report focused on confirmed impact first; put unverified leads in Potential Risks only when they are high-value enough to guide follow-up.
- For JS-heavy targets, include a coverage statement before claiming hidden-endpoint coverage. State which JS/interface sources were explored, such as rendered scripts, bundles, source maps, route manifests, dynamic imports, browser network traces, robots/sitemap, and archived routes. If those sources were not exhausted, say JS review was sampled/limited and do not claim complete coverage.

## index.md Format

Use this exact structure in the body of `index.md`:

```
## Summary

One paragraph overview: what was scanned, key stats (targets, services, vulns found), overall risk assessment.
Count confirmed vulnerabilities separately from unverified leads. Strikethrough loots are not vulnerabilities.

## Critical Loots

List verified loots first. Unannotated scanner matches may appear only with "unverified scanner match" stated clearly.
For each:
- **[target]** — vulnerability description, CVE if applicable, impact, verification status, link to findings/<id>.md with the reproducible PoC

## Potential Risks (Unverified)

Sniper intelligence, inconclusive verification, or scanner leads that lack active confirmation.
- **[target]** — what was detected, why it warrants investigation, manual verification step

## Services & Fingerprints

Brief list of discovered services and notable fingerprints (focus on security-relevant ones).

## Weak Credentials

List any discovered weak passwords/credentials. Note verification status.

## Dismissed Leads

Leads that were actively verified and determined to be false positives (strikethrough items).
Brief list so the reader knows what was checked and cleared.

## Recommendations

3-5 prioritized remediation actions based on confirmed loots.
```

## Rules

- Be concise. Each section should be 2-5 lines max.
- Only include sections that have relevant content.
- Do not invent loots not present in the scan data.
- Prioritize by severity: critical > high > medium.
- Use plain markdown, no code fences around the report.
- If no significant loots remain after applying verification filters, say so clearly. An honest "no confirmed vulnerabilities" is far more valuable than inflated severity.
