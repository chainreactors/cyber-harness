---
type: Tool Playbook
title: mitm
description: Run an AIScan Bash pseudo-command with HTTP(S) interception, then list, inspect, and analyze the captured request and response flows.
tags: [runtime, proxy, traffic]
status: stable
generated: { by: process:tool-registry-description, at: 2026-08-02T00:00:00Z }
---

# mitm — Traffic Capture

Use `mitm` when a scanner or browser workflow must be observed at the HTTP
request/response layer.

## Usage

```text
mitm <command> [args...]
mitm flows [--host <host>] [--last <n>]
mitm flow <id>
mitm analyze [--host <host>] [--last <n>]
mitm clear
```
