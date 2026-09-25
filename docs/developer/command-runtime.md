# Command runtime

`core/tool.RunCommand` owns one expanded argv. A registered `Command` with that
name wins over a program in `PATH`; registry admission, hooks, and errors remain
owned by `CommandRegistry`. Otherwise, `RunCommand` resolves `PATH` from the
call's environment and starts an OS program with its directory, streams, and
environment. Both branches return the command's details or error to the caller.

`BashTool` owns shell text and the outer `proc.Manager` session. It parses Bash
syntax once and runs `mvdan.cc/sh/v3/interp` in that session. The interpreter
owns expansion, shell builtins, redirection, pipelines, conditional execution,
and shell exit status. Each external command node calls `RunCommand`; registry
commands do not require aliases, PATH shims, another Cyber process, or an IPC
protocol. Shell builtins follow the interpreter's normal precedence.
An independently launched shell or REPL is an OS process: commands typed inside
it use that process's PATH and cannot call in-process registry commands. Shell
scripts given to `BashTool` can call registry commands at any composition depth.

`tmux` owns session control. A single literal external program in
`new-session` runs on `proc.TTY` with a real PID, input, resize, and terminal
signals. A script or registered command uses the managed interpreter session.
Detached sessions transfer their cancellation scope to the process manager.
The `proc.Info.Status` field records a logical interpreter exit code while
`Proc` remains nil; real PTY processes retain `Proc.ExitCode`.

The proxy hub owns the stable local proxy address. Persistent `proxy switch`,
`auto`, and `clear` change `State` for future default connections. A
`proxy <url> <command>` call instead owns a tokenized egress lease and passes
that route through its child `Execution`. The hub chooses its upstream dial
from the token on each request; an expired token fails closed. `mitm` and
`proxy` both invoke the same `RunCommand` function. A wrapped detached `tmux`
session takes ownership of its route lease until the session ends.

`third_party/proc` is based on `github.com/chainreactors/utils/proc`
`v0.0.0-20260917082019-d9b6bc48f7e2` and adds logical session status.
`third_party/mitmproxy` is based on `github.com/chainreactors/utils/mitmproxy`
`v0.0.0-20260909040842-68732c4ef873` and exposes the request's proxy-auth
identity to its dial callback. The root and audit modules use these local
copies until matching upstream module versions are available.
