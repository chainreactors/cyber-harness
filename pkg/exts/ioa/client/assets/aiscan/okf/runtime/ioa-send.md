---
type: Tool Playbook
title: ioa send
description: Send broadcasts, direct messages, replies, and structured security checkpoints to the current IOA collaboration space.
tags: [runtime, collaboration, ioa]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# ioa send — Collaboration Messages

`ioa send` publishes structured messages to the current IOA space or a specific
node (`--ref-nodes`), replies to messages (`--ref-messages`), and runs typed
protocol sends like `ioa send checkpoint`.

See the ioa skill (`aiscan://skills/ioa/SKILL.md`) for message formats and coordination rules.

## Related concepts

- Messages are sent within the space selected by [ioa space](ioa-space.md) and
  consumed through [ioa read](ioa-read.md).
- Checkpoints can report progress from the [scan pipeline](/easm/scan.md) or a
  recurring [loop](loop.md).
