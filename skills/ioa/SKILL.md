---
name: ioa
description: Use this skill when coordinating with other agents through IOA shared message spaces — the ioa pseudo-command (space/send/read subcommands), message envelope, and protocol skills (checkpoint, handoff, swarm, team).
internal: true
---

# IOA — Inter-Operator Async Collaboration

IOA provides shared message spaces for agent coordination through a single pseudo-command: `ioa` with `space`, `send`, and `read` subcommands.

Each aiscan instance binds to one space. After joining, all send/read operations automatically target that space — no space ID needed.

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
ioa send --ref-messages <message_id> --content '{"content": "confirmed, SQLi"}'       Reply to a message
ioa send checkpoint --kind verify --title "SQLi" --content "..." --target <url> --status confirmed
```

**CRITICAL**: The `--content` value must be a JSON object with a **`"content"` key** containing the message text. The swarm protocol parses `"content"` to route messages — any other key name (e.g. `"message"`, `"text"`) will be silently dropped. Additional fields (`"kind"`, `"targets"`, etc.) are optional metadata.

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

Loop workers do not receive peer messages automatically unless heartbeat is enabled. For situational awareness, poll intentionally with `ioa read --all --limit <N>` before and after long work, or use `ioa read --listen` for a live stream. If the worker was started with `--heartbeat`, the runtime periodically loads recent IOA messages into the heartbeat prompt.

## 2. Message Format

The envelope carries `content_type` (raw text when unset, or a protocol type like `checkpoint`/`handoff`/`swarm`/`team`) plus a JSON content body. **Every content body must have a `"content"` key** with the text body — except typed protocol bodies, which follow their own schema (see the protocol skills above; each also has `ioa://skills/<name>/schema.json`).

### Refs

- `reply --to <msg_id>`: reference a prior message (reply, follow-up)
- `to --node <node_id>`: address a specific node. Omit to broadcast to all space members.

## 3. Coordination Rules

1. **Read before write** — always `ioa read all` before starting work. A peer may have already claimed your target.
2. **Claim before work** — announce your scope before any significant operation.
3. **Share as you go** — emit loots immediately, not in a final batch. Peers need your data to make decisions now.
4. **No noise** — the space is shared memory, not chat. No "ok", "thanks", or thinking-out-loud.
5. **Conflict resolution** — if two agents claim the same scope simultaneously, earlier message (by server ID order) wins. The later agent adapts.

When coordinating workers from a heartbeat/coordinator role:

- **Workers are single-task** — they cannot respond to messages while busy. Do NOT send status checks; wait for the completion message.
- **Dispatch once, wait for completion** — send one task per worker, wait for their result before sending the next.
- **Do NOT scan targets yourself** — the coordinator only uses `ioa send`/`ioa read`; react to results and dispatch follow-ups.

## 4. Multi-Agent Swarm

For the full commander/node self-organization protocol (objective broadcast, squad formation, convergence), read `ioa://skills/swarm/SKILL.md`.
