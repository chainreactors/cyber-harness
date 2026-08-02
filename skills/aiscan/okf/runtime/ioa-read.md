---
type: Tool Playbook
title: ioa read
description: Read addressed, recent, or threaded messages from the current IOA collaboration space with cursor and limit controls.
tags: [runtime, collaboration, ioa]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# ioa read — Collaboration Inbox

`ioa read` retrieves messages from the current IOA space — addressed by
default, `--all` for everything, `--message` for thread context, `--after` for
cursor updates, `--listen` for a live stream.

See the ioa skill (`aiscan://skills/ioa/SKILL.md`) for message formats and coordination rules.

## Related concepts

- Read messages from the context selected by [ioa space](ioa-space.md) and
  reply or publish checkpoints with [ioa send](ioa-send.md).
- Agent messages may carry results from the [scan pipeline](/easm/scan.md).
