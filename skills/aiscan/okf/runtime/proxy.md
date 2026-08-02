---
type: Tool Playbook
title: proxy
description: Pseudo-command for managing proxy nodes and proxied command execution. Supports direct proxy URLs, Clash subscription feeds, and adaptive load balancing.
tags: [runtime, proxy]
status: stable
generated:
  actor: process:okf-migration
  at: 2026-08-02
---

# proxy - Proxy Node Management

`proxy` is a pseudo-command for managing proxy nodes and proxied command execution. It supports direct proxy URLs, Clash subscription feeds, and adaptive load balancing.

## Commands

```
proxy <proxy-url> <command> [args...]   Run a command through the specified proxy
proxy auto <url> [options]              Subscribe + adaptive load balancing (recommended)
proxy subscribe <url>                   Fetch a Clash subscription and list nodes
proxy list                              List loaded proxy nodes
proxy switch <name|index>               Switch the active proxy node
proxy test [name|index]                 Test proxy node connectivity
proxy current                           Show the current active proxy
proxy clear                             Clear subscription and revert to original proxy
```

## Proxy-Chain Examples

```bash
proxy socks5://127.0.0.1:1080 gogo -i 10.0.0.1 -p top2
proxy trojan://pass@host:443 zombie -i 10.0.0.1 -s ssh
proxy 6 gogo -i 10.0.0.1 -p top2           # Use subscribed node #6
proxy HK gogo -i 10.0.0.1                   # Use first node matching "HK"
```

## Auto Mode

Auto mode subscribes to a Clash feed and manages nodes with load balancing:

```bash
proxy auto https://example.com/clash-sub
proxy auto https://example.com/clash-sub -t trojan,vless
proxy auto https://example.com/clash-sub -c HK,JP -s adaptive
```

Options:
- `--type,-t`: Filter by protocol type (trojan, vless, ss, etc.)
- `--name,-n`: Filter by node name keyword
- `--country,-c`: Filter by server IP country (ISO 3166-1 alpha-2)
- `--strategy,-s`: Load balance strategy (adaptive, url-test, round-robin, random)

## Supported Protocols

Direct URLs: `socks5://`, `http://`, `trojan://`, `vless://`, `anytls://`, `hysteria2://`, `ss://`

Subscription: Clash YAML format with proxy node definitions.
