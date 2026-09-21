# Tools and commands

`Tool` and `Executor` define model-facing tool calls. `Command` and `CommandExecutor`
define native commands with arguments, streams and managed process execution.
`ToolRegistry` and `CommandRegistry` are independent extension resources; each
publishes its own typed contribution point and executor. They share the named
store and draining mechanism in `core/registry` while retaining their execution contracts.

Call `NewToolRegistry` and `NewCommandRegistry` explicitly when composing a host.
Extensions contribute declarations during `Load`; each contribution is revoked
and drained before its owner's resources are released. `ExecuteToolRequest`
adapts protocol tool calls, correlates progress and bounds inline result output.

The `hooks` subpackage uses AOP payloads and generic hook points, so registries
can invoke it without a dependency cycle. Process sessions are owned by `core/proc`.
