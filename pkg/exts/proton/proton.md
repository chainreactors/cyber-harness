---
type: tool
name: proton
---

# Proton content leak detection

Use the bash tool: `proton -i . -c keys -j --no-stats`.
`proton --template-list -c keys` lists active rules. `proton --help` lists filters.
Select custom templates with `-t`, rule IDs with `--id`, or regexes with `-e`.
The keys category is the default. The extension excludes audit output directories
when configured by the audit distribution.

JSON Lines groups results by rule and file; inspect every item in `events` for
individual occurrences and line positions. A rule match is a candidate leak:
check context, examples, public keys, placeholders and credentials actually in use.
Do not reproduce complete secrets in reports; retain minimal redacted evidence.

This scans files and supplied content. It does not walk Git history, attribute
commits, verify live credentials, or provide full Gitleaks baseline/allowlist parity.
To inspect a historical revision, deliberately export the relevant content and
record its commit separately. Scanning `.git` files is not history scanning.
