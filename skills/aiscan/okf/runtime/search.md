---
type: Tool Playbook
title: cyberhub
description: Search and list loaded fingerprints and POC templates with association-aware fingerprint-to-POC mapping. Available as cyberhub or search cyberhub.
tags: [runtime, search]
status: stable
generated: { by: process:okf-migration, at: 2026-08-02T00:00:00Z }
---

# cyberhub (also: search cyberhub)

Search and list loaded fingerprints and POC templates. Queries use the association index for fingerprint→POC mapping when structured filters are provided.

`cyberhub` is available both as a standalone command and as `search cyberhub`. Both forms are identical.

## Quick examples (use these patterns)

```bash
# Association-aware fingerprint→POC lookup
cyberhub search --finger tomcat
cyberhub search --finger shiro --severity critical,high

# CVE lookup
cyberhub search --cve CVE-2021-44228
cyberhub search --cve CVE-2020-1938

# Vendor/product lookup
cyberhub search --vendor apache --product tomcat
cyberhub search --product spring

# Only fingerprints that have POCs
cyberhub search finger --poc

# Entity detail + associations
cyberhub id tomcat
cyberhub id CVE-2021-44228

# Full-text search (existing)
cyberhub search poc seeyon
cyberhub search finger nginx

# List with filters
cyberhub list poc --severity critical,high
cyberhub list finger --limit 20

# JSON output
cyberhub search --finger shiro -j
```

## Full syntax

```bash
cyberhub list [finger|poc|all] [options]
cyberhub search [finger|poc|all] <query> [options]
cyberhub id <name-or-id>
```

Options:
- `--finger`: Filter by fingerprint (association-aware: alias + CPE links).
- `--cve`: Filter by CVE ID.
- `--vendor`: Filter by vendor name.
- `--product`: Filter by product name.
- `--poc`: Only show entries with associated POC templates.
- `-s, --severity`: Filter POCs by severity (critical, high, medium, low).
- `--tag`: Filter by tag. Can be comma-separated or repeated.
- `--protocol`: Filter fingerprints by protocol: http or tcp.
- `--limit`: Maximum rows (default: 50, 0 for all).
- `-j, --json`: Output JSON Lines.

## Common mistakes to avoid

```bash
# WRONG — these flags don't exist:
cyberhub -t seeyon           # -t is not a top-level flag
cyberhub --search ecology    # --search is not a flag
cyberhub --type poc -k struts # -k doesn't exist

# RIGHT:
cyberhub search poc seeyon
cyberhub search poc ecology
cyberhub search poc struts --tag rce
```

**Note**: `cyberhub` is a pseudo-command — do NOT append `2>/dev/null`, pipe to `head`/`grep`, or use shell redirections. Output is returned directly in the tool result.
