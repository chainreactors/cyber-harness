---
type: Tool Playbook
title: ioa space
description: Join or create an IOA collaboration space and inspect its available spaces, nodes, and conversation topics.
tags: [runtime, collaboration, ioa]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# ioa space — Space Management

`ioa space <name> <description>` selects the shared collaboration space used by
`ioa send` and `ioa read`. `ioa space list|nodes|topics` inspects spaces and
current-space members.

See the ioa skill (`aiscan://skills/ioa/SKILL.md`) for the complete coordination model.

## Related concepts

- A selected space is the shared context used by [ioa send](ioa-send.md) and
  [ioa read](ioa-read.md).
- Space members can coordinate work around the [scan pipeline](/easm/scan.md).
