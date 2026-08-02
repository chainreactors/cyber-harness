---
type: Tool Playbook
title: mitm
description: Run an AIScan Bash pseudo-command with HTTP(S) interception, then list, inspect, and analyze the captured request and response flows.
tags: [runtime, proxy, traffic]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
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

## Related concepts

- MITM captures HTTP traffic produced by the [scan pipeline](/easm/scan.md),
  [neutron](/easm/neutron.md), and [playwright](/easm/playwright.md).
- Combine it with [proxy](proxy.md) when the observed command also requires
  routed execution.
