---
type: Tool Playbook
title: loop
description: Schedule recurring agent prompts with cron expressions or duration intervals, and list or stop the active recurring tasks.
tags: [runtime, scheduling, automation]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# loop — Recurring Task Scheduler

Use `loop` for periodic monitoring and follow-up work that should run inside the
current agent runtime.

## Usage

```text
loop <schedule> <prompt>
loop list
loop stop <name>
loop stop-all
```

## Related concepts

- A loop can schedule recurring [scan](/easm/scan.md) or
  [fetch](fetch.md) workflows.
- Use [ioa send](ioa-send.md) to publish recurring checkpoints and
  [tmux](tmux.md) to inspect long-running command sessions launched by a task.
