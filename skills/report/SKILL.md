---
name: report
description: Use this skill to generate a concise security scan report from scan results in natural language.
---

# Report

Generate a concise security scan report from the provided scan results.

## Report Format

Use this exact structure:

```
## Summary

One paragraph overview: what was scanned, key stats (targets, services, vulns found), overall risk assessment.

## Critical Findings

List only confirmed high/critical findings. For each:
- **[target]** — vulnerability description, CVE if applicable, impact

## Services & Fingerprints

Brief list of discovered services and notable fingerprints (focus on security-relevant ones).

## Weak Credentials

List any discovered weak passwords/credentials.

## Recommendations

3-5 prioritized remediation actions based on findings.
```

## Rules

- Be concise. Each section should be 2-5 lines max.
- Only include sections that have relevant content.
- Do not invent findings not present in the scan data.
- Prioritize by severity: critical > high > medium.
- Use plain markdown, no code fences around the report.
- If no significant findings, say so clearly in one sentence.
