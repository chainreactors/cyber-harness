# Agent harness extension

`./agent` provides the standard loop, inbox, evaluator and execution mechanisms.
`pkg/exts/agent` installs those capabilities and owns the optional session host.
There is one `Extension`, one published `Runtime`, and no separate session extension.

`New(Config)` is inert. Supply `Loop` for a loop-only installation; additionally
supply `Application` and `Option` to enable sessions. A session host with a nil
loop supports history and control commands without reasoning. Constructors do not
open history, subscribe to events or start tasks.

Only `Extension.Load` and `Extension.Close` control installation lifetime.
`Runtime()` exposes session operations and also implements `agent.Loop`; the
Runtime cannot close its owner. The extension does not create
an inner extension graph or close the injected App resources.

`Deliver(ctx, inbox.Message)` admits peer or other external input into the primary
or sole open session. It rejects unopened, ambiguous, full or closing destinations;
it never creates sessions. IOA subscription and handoff belong to the independent
IOA client extension. Node names and prompt preambles are supplied by the profile.

Load binds execution to `Scope.Lifetime`, not the initialization context. Close
seals loop admission, cancels sessions and direct loop calls, and waits for actual
completion. A deadline limits waiting only: the owning Set retains dependencies
until a subsequent Close confirms drain. Session handles retain instance identity;
closed IDs cannot be reused before cleanup and terminal publication finish.

Sessions own their inbox, ordered queue, scheduler and execution state. History
inputs and snapshots are deep copies. Evaluation uses the underlying agent
mechanisms through the same admitted loop. `/clear` and `/compact` have one
session-rotation implementation. Local cancellation does not stop other sessions.

Observer failures are isolated and reported by the canonical event stream; they
do not turn completed session cleanup into a failure or suppress other observers.
A loop panic inside a session becomes a failed Run through the normal completion
path, so queued commands and extension drain remain usable. Direct Runtime.Run
callers still receive the loop's panic after its admission count is released.

The existing AOP protocol, history format and command exposure remain unchanged.
`Observe` reads the application's canonical event stream; output remains the
independent eventoutput extension. Console and Node consume Runtime, never the
mutable internal Agent. Scan policy belongs to product composition, not this host.

`Config.Commands` adds slash commands using the existing protobuf `CommandSpec`
and a session handler. Declarations are cloned and duplicate names/aliases are
rejected during construction. Dispatch, runtime help, Console completion and
remote catalogs all project the installed declarations. Remote advertisement is
explicit; the existing built-in remote catalog remains status/clear/compact.
Native `!` commands keep their separate command registry.

`FlagGroups` publishes the existing typed Agent options before argument parsing.
The CLI collects these inert groups without loading an extension, so help,
aliases, defaults and configuration precedence do not depend on runtime startup.
