---
type: Tool Playbook
title: loop
description: Schedule recurring agent prompts with cron expressions or duration intervals, and list or stop the active recurring tasks.
tags: [runtime, scheduling, automation]
status: stable
generated: { by: process:tool-registry-description, at: 2026-08-02T00:00:00Z }
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
