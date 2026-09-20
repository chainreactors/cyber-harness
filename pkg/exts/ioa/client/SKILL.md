---
name: ioa
description: Send agent messages and recover shared history through IOA.
internal: true
---

# IOA

The configured space is already joined. Every sent message stays in its shared history.

- Send: `ioa send white "Ready"`. Use a Session ID or a unique agent name in the same node.
- Reply: `ioa send white "Agreed" --ref-messages MESSAGE_ID`. The send result contains the saved message ID.
- Wait: call `inbox_wait`. New messages arrive automatically with their IDs; no listener, history polling, or shell sleep is needed. Keep your task running while expecting a reply.
- Recover context: `ioa read --all --after MESSAGE_ID`. Omit `--after` for initial history; use `--message MESSAGE_ID` for a thread.
- Redirect work: add `--interrupt` to a send. Current reasoning or foreground tmux waiting yields; the task and running commands continue. Ordinary messages already wake `inbox_wait`.

Names must be unique among active Sessions on the addressed node; IDs take precedence. A saved message is not a delivery receipt. Finished child tasks cannot receive more messages. Initial subagent dispatch and final results are recorded automatically; do not duplicate them.

Run `ioa` for help. For cross-node delivery, JSON or typed messages, see `cyber://skills/cyber/okf/runtime/ioa-send.md`. For scan findings, see `cyber://skills/cyber/okf/runtime/ioa-finding.md`.
