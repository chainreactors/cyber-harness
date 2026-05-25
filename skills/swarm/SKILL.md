---
name: swarm
description: Use when multiple aiscan agents collaborate in a shared IOA space without a central controller. Defines the autonomous coordination protocol — semantic self-introduction, target negotiation, work claiming, finding sharing, and convergence.
---

# Swarm — Autonomous Multi-Agent Coordination

This skill defines how multiple aiscan agents self-organize in a shared IOA space. There is no central controller — coordination emerges from agents reading each other's messages and making local decisions.

Prerequisite: the `ioa` skill covers IOA tool mechanics (`ioa_space`, `ioa_send`, `ioa_read`). This skill covers the **social protocol** on top of those tools.

## 1. Joining the Swarm

When you enter a space:

1. **Read first.** `ioa_read all=true limit=100` — understand who's here, what's been claimed, what's been found.
2. **Introduce yourself.** Send a single message describing:
   - Your name and node ID
   - What tools and skills you have, listing only commands available in your runtime (e.g. "I have gogo, spray, neutron, zombie"; full builds may also have katana/passive)
   - What you're best at (e.g. "strong at web surface — spray, fuzz"; add katana only when available)
   - Your current status ("idle, ready for work")

This is a **semantic registration** — other agents' LLMs read your introduction and understand your capabilities through language, not structured fields. Be specific about what you can actually do; don't list tools you don't have.

Example:
```json
{"kind": "profile", "content": "I'm scanner-01. Tools: gogo, spray, neutron, zombie. Strongest at web surface analysis — spray fingerprinting and fuzz for injection points. Currently idle."}
```

3. **Read peer introductions.** After announcing, read again to see if anyone else introduced themselves while you were writing. Build a mental model: who can do what.

## 2. Situational Awareness

Before starting any work phase, always check the space:

- **Who is online?** Look for `kind: profile` messages. Note each peer's stated capabilities.
- **What's been claimed?** Look for `kind: claim` messages. Don't touch claimed targets.
- **What's been found?** Look for `kind: asset` and `kind: finding` messages. Build on others' discoveries instead of re-scanning.
- **What's blocked?** Look for `kind: blocker` messages. Maybe you can help.
- **What's done?** Look for `kind: result` and `kind: handoff` messages.

Rule: **never start a long operation without reading the space first.** A 5-second `ioa_read` can save 5 minutes of redundant scanning.

## 3. Target Negotiation

When new targets appear (from a dispatcher, from the initial prompt, or discovered by a peer):

**Small target set (1-3 targets):**
- First agent to read the targets proposes a split and claims their portion in one message.
- Second agent reads, takes the unclaimed portion, announces.
- No negotiation needed — claim and go.

**Large target set (many IPs, subnets, or domains):**
- Propose a split strategy based on your strengths:
  - **By capability**: "I'll handle web surface (spray+katana), you take network services (gogo+zombie)"
  - **By target range**: "I'll take 10.0.0.0/25, you take 10.0.0.128/25"
  - **By domain**: "I'll take *.api.example.com, you take *.admin.example.com"
  - **By phase**: "I'll do passive recon for all targets, hand off IPs to you for active scanning"
- Keep negotiation to **1-2 messages max**. Propose, acknowledge, start. Don't debate.

**Conflict resolution:**
- If two agents claim the same target simultaneously, the earlier message (by server timestamp / message ID order) wins.
- The later agent reads, sees the conflict, acknowledges, and picks different work.
- Don't argue about who claimed first — just read the message order and adapt.

Example negotiation:
```
Agent A: {"kind": "claim", "scope": "web surface for 10.0.0.0/24", "tools": ["spray", "katana", "neutron"], "eta_min": 10}
Agent B: {"kind": "claim", "scope": "network services for 10.0.0.0/24", "tools": ["gogo", "zombie"], "eta_min": 8}
```
Two messages. Both agents start working.

## 4. Claiming Work

Before starting any significant operation:

1. **Send a claim**: `{"kind": "claim", "scope": "<what you're about to do>", "eta_min": <rough estimate>}`
2. **Start working immediately** — don't wait for acknowledgment. Claims are announcements, not requests.
3. **If you see an existing claim** for the same scope, skip it and find other work.
4. **If a claimer goes silent** (no messages, no findings, no progress for much longer than their ETA), you may take over. Announce: `{"kind": "claim", "scope": "<same scope>", "note": "taking over from <peer>, no activity since their claim"}`

Claims are lightweight coordination, not locks. The goal is avoiding obvious duplication, not guaranteeing mutual exclusion.

## 5. Sharing Findings

Share discoveries **as you produce them**, not in a batch at the end. Other agents need your findings to make decisions now.

What to share:
- **Assets discovered**: `{"kind": "asset", "ips": [...], "domains": [...], "source": "passive/fofa"}`
- **Vulnerabilities found**: `{"kind": "finding", "severity": "high", "target": "...", "vuln": "...", "evidence": "..."}`
- **Dead ends**: `{"kind": "finding", "severity": "info", "target": "...", "note": "no web services, all ports filtered"}`
- **Phase transitions**: `{"kind": "handoff", "from_phase": "recon", "next": "scan", "context": {"asset_count": 47}}`
- **Blockers**: `{"kind": "blocker", "reason": "fofa quota exhausted", "need": "someone run hunter or shodan source"}`

When you see a peer's findings:
- If they found assets in your claimed scope, acknowledge and incorporate.
- If they found a vulnerability related to your work, investigate or verify it.
- If they're blocked on something you can help with, help.

## 6. Work Cycle

Your workflow is a **continuous loop**, not a one-shot sequence. The initial negotiation (2 rounds max) only decides the first split — after that, you keep cycling:

```
read space → claim → work (share findings as you go) → complete → read space → ...
```

### After completing a claim

1. **Report**: send `{"kind": "result", "scope": "<your scope>", "summary": "...", "findings_count": N}`
2. **Read**: `ioa_read all=true` — what happened while you were working?
   - Did peers discover new targets or assets you should follow up on?
   - Did a peer hand off work to you (`kind: handoff`)?
   - Did a peer hit a blocker you can unblock?
   - Are there unclaimed targets remaining?
3. **Decide next action**:
   - **New work available**: claim it and start the cycle again.
   - **Peer needs help**: take a sub-task from their scope, announce it.
   - **Deeper analysis needed**: your own findings may warrant a second pass (e.g., recon found 50 IPs → now do web analysis on the interesting ones). Claim the follow-up.
   - **Nothing left**: move to convergence (below).

### During work

Don't go silent. Even during a long scan, periodically:
- **Read**: check if peers sent findings or blockers relevant to your current work.
- **Write**: share intermediate discoveries — assets, open ports, fingerprints — as they come in. A peer waiting for your recon output shouldn't have to wait until you finish everything.

The rule: **every significant phase boundary is a read-write checkpoint.** Finished passive recon? Share assets, read space, then start active scanning. Finished port scanning? Share results, read space, then start web probing.

### Convergence

When there's genuinely no more work:

1. All agents have sent `kind: result` for their scopes.
2. No unclaimed targets, no pending handoffs, no unresolved blockers.
3. The last agent to finish (or the one with the broadest view) compiles a final summary:
   ```json
   {"kind": "summary", "total_findings": N, "critical": X, "high": Y, "agents": ["scanner-01", "scanner-02"], "note": "all scopes complete"}
   ```

Don't wait indefinitely for slow peers. If you've finished and a peer hasn't responded in a reasonable time, compile what you have and note the gap.

## 7. Collaboration Patterns

Choose based on the task shape. Announce your preferred pattern early — one message is enough.

### Two agents, one large target
Split by capability or target range. The agent with web tools takes HTTP/HTTPS surface; the other takes network services, weak credentials, and infrastructure.

### Two agents, diverse surface
Split by skill specialization. One agent focuses on passive recon + intelligence (passive, sniper, web_search), the other on active scanning (gogo, spray, neutron, zombie).

### Three or more agents
One agent naturally becomes the coordinator — typically the first to propose a plan. The coordinator suggests splits, others acknowledge and start. The coordinator also does work, not just coordination.

### Reviewer pattern
One agent does primary scanning, the other independently verifies high-severity findings using different tools or approaches. Higher confidence, lower throughput. Good for critical targets.

### Pipeline pattern
Agents specialize by phase: Agent A does recon (passive → gogo), hands off assets. Agent B does web analysis (spray → katana → fuzz). Agent C does exploitation verification (neutron → manual checks). Each agent starts working as soon as upstream findings arrive — don't wait for the full handoff.

## 8. Anti-patterns

- **Over-negotiating**: more than 2 messages before anyone starts working. Just claim and go — the other agent will adapt.
- **Waiting for permission**: claims are announcements. Don't ask "can I take X?" — say "I'm taking X" and start.
- **Silent work**: scanning for 10 minutes without sending a single finding or status. Peers can't coordinate with a silent agent.
- **Hoarding findings**: waiting until you're done to share everything at once. Share as you go.
- **Ignoring peer messages**: always `ioa_read all=true` before starting a new phase. Your peer may have found something that changes your approach.
- **Re-scanning claimed targets**: if a peer claimed it and is active, find something else to do.
- **Endless convergence**: spending more time summarizing than scanning. One summary message is enough.

## Quick Reference

| Moment | Action |
|--------|--------|
| Enter space | `ioa_read all=true`, then send profile |
| Before starting work | `ioa_read all=true`, check claims |
| Starting a task | Send `kind: claim` with scope and ETA |
| Found something | Send `kind: asset` or `kind: finding` immediately |
| Phase done | Send `kind: handoff` with context for peers |
| Stuck | Send `kind: blocker`, describe what you need |
| All work done | Send `kind: result` with summary |
| Everyone done | Compile `kind: summary` |
