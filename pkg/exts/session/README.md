# Session management extension

`pkg/exts/session` owns conversation sessions, runs, queues, inboxes, history
and session protocols. It is distinct from the Agent loop extension because a
session host can expose history and control without selecting a reasoning loop.

`New(Config)` is inert and receives concrete capabilities: App, options,
optional IOA Runtime and an optional admitted `agent.Loop`. It never imports or
closes their owning extensions. The composition root places Session after App
and Agent in the single `extension.Set`, so reverse shutdown drains Session
before either dependency.

Only `Extension.Load` and `Extension.Close` own lifecycle. `Runtime()` exposes
session operations without Load/Close. Load initializes history, prompt state
and subscriptions against `Scope.Lifetime`; Close seals new session admission,
cancels sessions and waits for runs, queues and subscriptions to drain. A close
deadline is retryable and does not release dependencies early.

Sessions own their inbox, ordered queue, scheduler and execution state. History
inputs and snapshots are deep copies. `/clear` and `/compact` share one session
rotation implementation, and cancellation of one session does not stop others.
Loop panic is converted to a failed Run through the normal completion path.

`Config.Commands` adds slash commands using the protobuf `CommandSpec` and a
session handler. Declarations are cloned and duplicate names or aliases fail
during construction. Console, Node and Web consume the published Runtime;
native `!` commands retain their separate command Registry.

`FlagGroups` publishes typed session/agent options before argument parsing, so
CLI discovery has no lifecycle side effects.
