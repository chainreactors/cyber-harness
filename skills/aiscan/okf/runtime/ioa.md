---
type: Tool Playbook
title: ioa
description: IOA shared message spaces for agent coordination via ioa_space, ioa_send, ioa_read pseudo-commands.
tags: [runtime, collaboration]
status: stable
generated:
  actor: process:okf-migration
  at: 2026-08-02
---

# IOA — Inter-Operator Async Collaboration

IOA provides shared message spaces for agent coordination. Three pseudo-commands: `ioa_space`, `ioa_send`, `ioa_read`.

Each aiscan instance binds to one space. After joining, all send/read operations automatically target that space — no space ID needed.

## 1. Tool API

### ioa_space

Manage spaces. Subcommands:

```
ioa_space join --name "case-target" --description "Your role"   Join or create a space
ioa_space list                                                  List available spaces
ioa_space nodes                                                 Show nodes in current space
ioa_space topics                                                Show root messages (conversation starters)
```

After `join`, the response includes member nodes (ID, name, description) and existing root messages.

### ioa_send

Send a message to the current space. Subcommands:

```
ioa_send --content '{"content": "recon complete, 3 hosts found"}'                          Broadcast to all
ioa_send to --node <node_id> --content '{"content": "scan 10.0.0.1 for web vulns"}'       Send to a specific node
ioa_send reply --to <message_id> --content '{"content": "confirmed, SQLi on /login"}'     Reply to a message
```

**CRITICAL**: The `--content` value must be a JSON object with a **`"content"` key** containing the message text. The swarm protocol parses `"content"` to route messages — any other key name (e.g. `"message"`, `"text"`) will be silently dropped. Additional fields (`"kind"`, `"targets"`, etc.) are optional metadata.

Use `--refs` for raw reference control.

### ioa_read

Read messages from the current space. Subcommands:

```
ioa_read                           Messages addressed to this node
ioa_read all --limit 50            All messages in the space
ioa_read thread --id <message_id>  Context (ancestors + descendants) of a message
ioa_read new --after <message_id>  Messages after a cursor (pagination)
```

Without `all`, only messages explicitly directed at your node are returned.

### Background Monitoring

There is no `ioa read --listen` pseudo-command in aiscan. Loop workers do not receive peer messages automatically unless heartbeat is enabled.

For situational awareness, poll intentionally with `ioa_read all --limit <N>` before and after long work. If the worker was started with `--heartbeat`, the runtime periodically loads recent IOA messages into the heartbeat prompt.

## 2. Message Format

Messages use structured JSON content. **Every message must have a `"content"` key** with the text body. Additional fields provide metadata:

```json
{"content": "Found SQLi on /login endpoint", "kind": "loot", "severity": "critical", "target": "http://10.0.0.1"}
```

Common `kind` values:

| kind | purpose | extra fields |
|------|---------|------------|
| `claim` | announce work you're about to start | `scope`, `eta_min` |
| `asset` | share discovered targets | `ips`, `domains`, `source` |
| `loot` | share vulnerabilities or dead ends | `severity`, `target`, `vuln`, `evidence` |
| `handoff` | signal phase transition | `from_phase`, `next`, `context` |
| `blocker` | request help | `reason`, `need` |
| `result` | report completed work | `scope`, `summary`, `loots_count` |

### Refs

- `reply --to <msg_id>`: reference a prior message (reply, follow-up)
- `to --node <node_id>`: address a specific node. Omit to broadcast to all space members.

### Task dispatch

To dispatch a task to a peer node:

```
ioa_send to --node <target_node_id> --content '{"content": "Scan 10.0.0.0/24 for web services", "kind": "task_dispatch", "targets": ["10.0.0.0/24"]}'
```

The `"content"` key is the task description the peer will execute. `"kind": "task_dispatch"` marks it as an actionable task.

## 3. Basic Coordination Rules

These apply in any multi-agent scenario:

1. **Read before write** — always `ioa_read all` before starting work. A peer may have already claimed your target.
2. **Claim before work** — send `kind: claim` with your scope before any significant operation.
3. **Share as you go** - emit loots immediately, not in a final batch. Peers need your data to make decisions now.
4. **No noise** — the space is shared memory, not chat. No "ok", "thanks", or thinking-out-loud.
5. **Conflict resolution** — if two agents claim the same scope simultaneously, earlier message (by server ID order) wins. The later agent adapts.

## 4. Multi-Agent Swarm

When working in a swarm (2+ agents, no central controller), read the full coordination protocol at `aiscan://skills/aiscan/reference/ioa-swarm.md`. It covers: semantic self-introduction, target negotiation strategies, work cycles, convergence criteria, and collaboration patterns (split-by-skill, pipeline, reviewer).
