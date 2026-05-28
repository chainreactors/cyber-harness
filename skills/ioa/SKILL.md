---
name: ioa
description: Use when collaborating with peer agents in a shared IOA space. Covers when to read partner messages, what to share, and how to converge on a strategy without a central controller. Read this before sending or reading IOA messages in a multi-agent session.
---

# IOA — Inter-Operator Async Collaboration

IOA gives you a shared message log with peer agents. You and your partner(s) see the same messages and can converge on a strategy without a central controller. The three tools are `ioa_space`, `ioa_send`, `ioa_read`.

Treat the space as **shared memory**, not chat. Every message persists and is visible to all peers in the space.

## When to Read

- **Joining a space**: see what's already been discussed, what's claimed, what's been found.
- **Before starting a major phase** (recon, scanning, exploitation): your partner may have already started or have findings you can build on.
- **After completing a phase**: check if the partner sent anything you should react to.
- **When stuck, or considering a slow / quota-consuming operation**: maybe the partner already did it.

Use `ioa_read --space_id "<space_id>" --all true` to see every message in the space, not just messages addressed to you. The `--space_id` parameter is **always required**. The default scope only returns messages explicitly directed at your node id.

## When to Send

- **You've decided to take on a chunk of work** — announce it so the partner can pick something else. This is the "claim" pattern.
- **You've found something concrete** — domains, IPs, vulnerabilities, dead ends. Share immediately, the partner may need it now.
- **You're handing off** — "I'm done with recon, here's the asset list, switching to scan".
- **You disagree with the partner's direction or notice a duplicate** — say so, don't silently re-do the work.
- **You finished the whole task** — emit a final result message so the dispatcher and partner know.

Do not send: chatty acknowledgments ("ok", "thanks"), thinking-out-loud, or status pings with no new information. The space is shared memory, not chat — noise costs everyone tokens.

## Message Content

Structured content beats prose. Use a `kind` field so readers can route on it. Useful kinds:

```json
{"kind": "claim", "scope": "recon for fjsmartedu.cn", "eta_min": 5}
{"kind": "asset", "domains": ["..."], "ips": ["..."], "source": "passive/fofa"}
{"kind": "finding", "severity": "high", "target": "https://...", "evidence": "..."}
{"kind": "handoff", "from_phase": "recon", "next": "scan", "context": {"asset_count": 47}}
{"kind": "blocker", "reason": "fofa quota exhausted", "need": "partner run hunter source"}
{"kind": "result", "summary": "...", "findings_count": 6}
```

When you reference a previous message (replying, building on, disagreeing with), set `refs.messages` to its id. When a message is meant for a specific peer, set `refs.nodes` to their node id — otherwise everyone in the space sees it.

### Dispatching a task to another node

To start a new task on a peer node (e.g. handing off scan results for vulnerability analysis), the content object **must** include a `content` key with the task description as a string, and set `meta.kind` to `task_dispatch`. Also set `refs.nodes` to the target node id.

```json
ioa_send --space_id "<space>" --content '{"content": "Run neutron and web_search CVE checks on 127.0.0.1:22 (OpenSSH 8.9p1) and 127.0.0.1:80 (Python SimpleHTTP, directory listing). Report findings.", "meta": {"kind": "task_dispatch"}, "targets": ["127.0.0.1"]}' --refs '{"nodes": ["<target_node_id>"]}'
```

The `content` string is required — the receiving node's swarm router reads this field to understand the task. Without it the message is silently dropped. Other fields (`claim`, `finding`, `asset`, etc.) are peer chatter and do not need the `content` string.

## Collaboration Patterns

Pick based on the task shape. Discuss the pick in the space briefly before committing — one message each is enough.

- **Split by phase**: one peer does recon (e.g. `passive` in full builds), the other waits for the asset handoff then runs scanning (`gogo` + `spray` + `neutron`). Lowest coordination overhead, best when phases are sequential.
- **Split by target**: divide IP ranges, subdomains, or subsidiaries. Best when the target set is large and uniform.
- **Split by skill**: one focuses on web surface (`spray`, optional `katana`, `neutron`), the other on network services (`gogo`, `zombie`). Best when the target has a diverse surface.
- **Reviewer pattern**: one does the primary work, the other independently verifies high-severity findings with different tools or sources. Higher confidence, lower throughput. Good for paranoid mode.

## Anti-patterns

- **Silent duplication**: both peers run the same recon phase on the same company. A cheap `ioa_read --space_id "<space_id>" --all true` upfront prevents this.
- **Over-coordination**: 10 messages debating who runs recon. Just take it and announce — the partner can read and react in one round-trip.
- **Race on claim**: both peers send "I'll do recon" at the same time. Whoever's message has the earlier server timestamp wins; the other peer reads, sees the conflict, and picks the other side of the split.
- **Withholding findings until the end**: defeats the point of shared memory. Send `asset` / `finding` messages as you produce them, not in a single dump.
- **Spamming refs.nodes for every message**: most messages should be broadcast (no `refs.nodes`) so the whole space sees them. Use `refs.nodes` only when you genuinely want to address one peer.

## Quick Reference

| Situation | Action |
|-----------|--------|
| Just joined a space | `ioa_read --space_id "<space_id>" --all true --limit 50` |
| About to start a long operation | `ioa_read --space_id "<space_id>" --all true`, then `ioa_send` a claim |
| Found something useful | `ioa_send` with `kind=asset` or `kind=finding` |
| Phase finished | `ioa_send` with `kind=handoff` |
| Stuck | `ioa_send` with `kind=blocker` |
| Task complete | `ioa_send` with `kind=result` |
