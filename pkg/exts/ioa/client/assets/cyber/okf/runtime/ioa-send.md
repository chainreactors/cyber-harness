---
type: Tool Playbook
title: ioa send
description: Send a message to an agent, reply to a message, or redirect current work.
tags: [runtime, collaboration, ioa]
status: stable
generated: { by: process:okf-maintain, at: 2026-08-02T11:46:25Z }
---

# ioa send — Collaboration Messages

`ioa send <session-id-or-name> "message"` sends to an active agent. Add
`--ref-messages ID` to reply, `--interrupt` to redirect work, or `--ref-nodes ID`
for another node. New messages arrive automatically; call `inbox_wait` to wait.

The result contains the saved message ID. Use `--ref-messages ID` to record a
reply link; mentioning an ID in the text alone does not create that link.

For advanced messages:

- `ioa send SESSION --content '{"text":"Ready"}'` accepts a JSON body.
- `ioa send --content '{"text":"Update"}'` publishes a node broadcast.
- `ioa send <protocol> --help` describes typed sends. Use a protocol only when
  the task requires it; schemas are in `ioa://skills/<protocol>/SKILL.md`
  (checkpoint, handoff, swarm, team).

The configured space is already joined. `ioa space NAME "description"` switches
it. Names must be unique on the addressed node; use a Session ID when ambiguous.
The legacy `--target-session SESSION` option remains compatible.
