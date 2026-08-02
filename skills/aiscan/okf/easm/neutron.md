---
type: Tool Playbook
title: neutron
description: Use this playbook when working with neutron for template-based POC execution, template filtering, and POC result analysis.
tags: [easm, poc]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# Neutron

Neutron is the template-based POC execution tool in aiscan.

## Primary workflow: Fingerprint to POC

When a fingerprint is discovered (from gogo, spray, or scan), run matching POCs:

```bash
neutron -u <target> --finger <name>
neutron -u <target> --finger <name> -s critical,high
neutron -u <target> --finger shiro --finger spring -c 5
```

The `--finger` flag queries the association index for comprehensive matching (direct links, alias names, CPE vendor/product). Without `--finger`, templates that require fingerprint context are skipped to prevent false positives.

## Preview selected templates

```bash
neutron --finger tomcat --template-list
neutron --finger nginx -s critical --template-list -j
```

## Direct template execution

```bash
neutron -u <target> --id <template-id>
neutron -u <target> -s critical,high
neutron -u <target> --tags cve,rce -c 10 --rate-limit 20
```

## Custom templates

```bash
neutron -u <target> -t ./pocs
neutron -u <target> -t ./pocs --id custom-poc
neutron -u <target> -t ./pocs --restrict-templates
```

## Notes

- Severity is template metadata.
- A match is scanner evidence; user intent decides whether to summarize, triage, verify, correlate, or report it.

## Related concepts

- Neutron consumes fingerprints from [gogo](gogo.md), [spray](spray.md), and
  the [scan pipeline](scan.md), with associations resolved by
  [cyberhub](/runtime/search.md).
- Capture HTTP template execution with [mitm](/runtime/mitm.md); browser-based
  templates can be recorded or replayed with [playwright](playwright.md).
