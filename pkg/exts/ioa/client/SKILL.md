---
name: ioa
description: Use this skill when coordinating with other agents through IOA shared message spaces — the ioa pseudo-command (space/send/read subcommands), message envelope, and protocol skills (checkpoint, handoff, swarm, team).
internal: true
---

# IOA — Inter-Operator Async Collaboration

IOA provides shared message spaces for agent coordination through a single pseudo-command: `ioa` with `space`, `send`, and `read` subcommands.

With the IOA extension installed, an empty URL uses a process-local memory space without an HTTP listener; an external URL uses that server. Each cyber instance binds to one space. After joining, all send/read operations automatically target that space — no space ID needed.

The wire protocol (message envelope, typed content formats) is defined by the `chainreactors/ioa` module. Its protocol skills are loaded as internal skills — read them for exact message formats:

- `ioa://skills/checkpoint/SKILL.md` — human-in-the-loop review (`content_type: "checkpoint"`)
- `ioa://skills/handoff/SKILL.md` — fire-and-forget delegation (`content_type: "handoff"`)
- `ioa://skills/swarm/SKILL.md` — commander/node self-organization (`content_type: "swarm"`)
- `ioa://skills/team/SKILL.md` — named-group broadcast (`content_type: "team"`)

## 1. Tool API

The command surface is the ioa module's CLI (`github.com/chainreactors/ioa/client` go-flags commands), with the current space auto-injected as `--space` on send/read.

### ioa space

Join or create a space, and inspect spaces:

```
ioa space "case-target" "Your role" [--tag recon]   Join or create a space (sets it as current)
ioa space list                                      List available spaces
ioa space nodes                                     Show nodes in current space
ioa space topics                                    Show root messages (conversation starters)
```

After joining, the response includes member nodes (ID, name, description) and existing root messages.

### ioa send

Send a message to the current space:

```
ioa send --content '{"content": "recon complete, 3 hosts found"}'                     Broadcast to all
ioa send --ref-nodes <node_id> --content '{"content": "scan 10.0.0.1 for web vulns"}' Send to a specific node
ioa send --target-session <session_id> --content '{"text": "follow-up for a sibling"}'  Send within this node
ioa send --ref-nodes <node_id> --target-session <session_id> --content '{"text": "follow-up"}'  Send to a remote session
ioa send --ref-messages <message_id> --content '{"content": "confirmed, SQLi"}'       Reply to a message
ioa send checkpoint --kind verify --title "SQLi" --content "..." --target <url> --status confirmed
```

The `--content` value must be a JSON object. Raw peer messages may use `"text"`; swarm messages use `"content"` for protocol routing. Typed protocols follow their own schemas.

Typed protocol sends (`ioa send <protocol> [flags]`) are registered by the ioa module — `checkpoint` supports `--kind`, `--title`, `--content`, `--target`, `--status`. Raw sends accept `--content-type`, `--meta`, and `--content-schema`.

### ioa read

Read messages from the current space:

```
ioa read                                Messages addressed to this node
ioa read --all --limit 50               All messages in the space
ioa read --message <message_id>         Context (ancestors + descendants) of a message
ioa read --message <id> --direction upstream|downstream   Thread traversal
ioa read --after <message_id>           Messages after a cursor (pagination)
ioa read --listen                       Stream new messages (SSE)
```

Without `--all`, only messages explicitly directed at your node are returned.

### Background Monitoring

Running Sessions receive addressed peer messages through their existing Inbox. Parent and child tasks share a Node ID; use the Session ID returned by `subagent create` or `subagent list` to address a child or sibling. Without a Session target, external messages enter the primary or sole ordinary Session. A finished child stops receiving; dispatch a new task to continue.

Initial dispatch and final result are automatically recorded as linked handoffs before execution and before completion notification. The initial record includes the task and actual input, never the inherited model history. Do not duplicate these records manually. `subagent.message` is removed; all ongoing communication uses `ioa send`.

A successful send returns a saved message ID, not a consumption receipt. Late joiners explicitly use `ioa read` for context; history is not automatically replayed. Local memory is lost when the extension closes. External failures do not switch to a private local space.

## 2. Message Format

The envelope carries `content_type` (raw text when unset, or a protocol type like `checkpoint`/`handoff`/`swarm`/`team`) plus a JSON content body. Raw messages can use a `"text"` field. Swarm messages use `"content"`; typed protocol bodies follow their own schema (see the protocol skills above; each also has `ioa://skills/<name>/schema.json`).

### Refs

- `--ref-messages <msg_id>`: reference a prior message (reply, follow-up)
- `--ref-nodes <node_id>`: address a specific node. Omit to broadcast to all space members.

## 3. Coordination Rules

1. **Read before write** — always `ioa read --all` before starting work. A peer may have already claimed your target.
2. **Claim before work** — announce your scope before any significant operation.
3. **Share as you go** — emit loots immediately, not in a final batch. Peers need your data to make decisions now.
4. **No noise** — the space is shared memory, not chat. No "ok", "thanks", or thinking-out-loud.
5. **Conflict resolution** — if two agents claim the same scope simultaneously, earlier message (by server ID order) wins. The later agent adapts.

When coordinating workers from a heartbeat/coordinator role:

- **Workers receive while running** — addressed input enters their Inbox and is consumed at the next loop boundary. Delivery does not interrupt an active model/tool call or guarantee a reply.
- **Dispatch once, wait for completion** — use one task per child. A completed child is closed; dispatch a fresh child for later work.
- **Do NOT scan targets yourself** — the coordinator only uses `ioa send`/`ioa read`; react to results and dispatch follow-ups.

## 4. Multi-Agent Swarm

For the full commander/node self-organization protocol (objective broadcast, squad formation, convergence), read `ioa://skills/swarm/SKILL.md`.

## Scan collaboration

In IOA collaboration mode, use `ioa send checkpoint` for collaboration reports. Publish verified findings using `cyber://skills/cyber/okf/runtime/ioa-finding.md`; keep local reports and their result IDs as the durable record.
