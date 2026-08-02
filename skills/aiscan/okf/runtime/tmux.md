---
type: Tool Playbook
title: tmux
description: PTY session manager built into aiscan. All bash commands run inside tmux sessions; long commands auto-background with inbox delivery.
tags: [runtime, session]
status: stable
generated: { by: process:okf-migration, at: 2026-08-02T00:00:00Z }
---

# tmux - Session Manager

tmux is the PTY session manager built into aiscan. All `bash` commands run inside tmux sessions. Commands completing within 15 seconds return output inline; longer commands are auto-backgrounded with incremental output delivered to the agent inbox automatically.

## Commands

```
tmux new-session [-d] [-s name] [--timeout duration] "command"
tmux ls
tmux capture-pane -t <id> [-n lines] [-c bytes] [--full]
tmux send-keys -t <id> "text" [Enter|C-c|C-d]
tmux kill-session -t <id>
tmux wait-for -t <id> [--timeout duration]
```

## Output Reading Modes

### Incremental (default)
```
tmux capture-pane -t <id>
```
Returns only new output since the last read. Never duplicates. Advances an internal cursor. Use this for monitoring progress step by step.

### Last N Lines
```
tmux capture-pane -t <id> -n 50
```
Returns the last 50 lines from the buffer. Does not affect the incremental cursor. Can be called repeatedly without duplication concerns since the semantics are explicit.

### Last N Bytes
```
tmux capture-pane -t <id> -c 4096
```
Returns the last 4096 bytes from the buffer. Useful when you need raw tail output regardless of line boundaries (e.g. binary or dense output).

### Full Buffer
```
tmux capture-pane -t <id> --full
tmux capture-pane -t <id> --full -n 100
```
Re-reads the entire buffer (or last N lines of it). Use sparingly; prefer incremental or `-n` for most reads.

## Auto-Background & Inbox Monitoring

When a `bash` command exceeds 15 seconds, it is automatically backgrounded:
1. The bash tool returns immediately with the session id.
2. A **monitor goroutine** starts, pushing incremental output to the agent inbox every 10 seconds as `<session_output>` messages.
3. When the session completes, a `<session_completion>` message is pushed to inbox with exit code and last 20 lines.

This means for long-running commands:
- **You do not need to poll** with `tmux capture-pane`. Output arrives automatically via inbox.
- Wait for inbox messages to review progress, then decide next action.
- Use `tmux capture-pane -t <id>` only if you need to inspect output between inbox deliveries.
- Use `tmux kill-session -t <id>` to stop early.

## Patterns

### Long scan — let monitoring deliver output
```
bash: gogo -i 10.0.0.0/24 -p top2
# auto-backgrounded → session id returned
# wait for inbox <session_output> messages with scan progress
# wait for inbox <session_completion> when done
```

### Interactive session — send keys
```
bash: tmux new-session -d -s db "mysql -h target -u root"
bash: tmux send-keys -t db "SHOW DATABASES;" Enter
bash: tmux capture-pane -t db
bash: tmux kill-session -t db
```

### Check recent output without advancing cursor
```
bash: tmux capture-pane -t <id> -n 20
```

### Quick tail of raw bytes
```
bash: tmux capture-pane -t <id> -c 2048
```
