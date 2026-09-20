---
type: Tool Playbook
title: ioa read
description: Recover earlier messages or a reply thread from the shared IOA history.
tags: [runtime, collaboration, ioa]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# ioa read — Shared History

`ioa read --all --after ID` recovers messages after a known message. Omit `--after`
for initial history, or use `--message ID` for a thread. New messages already arrive
automatically: call `inbox_wait` to wait, without polling or extra listeners.

Add `--limit N` to bound the result. Without `--all`, only messages addressed to
your node are returned. `ioa read --help` lists the remaining options.
