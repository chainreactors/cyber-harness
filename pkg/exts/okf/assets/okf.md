---
type: Tool Playbook
title: okf
description: Validate Open Knowledge Format 0.2 bundles and run stricter production checks.
tags: [okf, markdown, validation]
status: stable
generated: { by: process:aiscan, at: 2026-09-18T00:00:00Z }
---

# OKF validation

Use `okf validate <path>` for official OKF 0.2 conformance. It checks UTF-8 Markdown, YAML frontmatter, required concept types, reserved index and log files, lifecycle, provenance, and Attested Computation fields.

Use `okf test <path>` before publishing a maintained bundle. It additionally requires recommended metadata, valid internal links, index coverage, and complete computation references.

Both commands accept `--format text|json`. They inspect files only and never execute an OKF executor or attester.
