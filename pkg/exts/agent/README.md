# Agent loop extension

`agent/` contains the loop algorithm. `pkg/exts/agent` adapts one selected
`agent.Loop` to the profile lifecycle without adding session concerns.

`New(loop)` is inert. `Extension.Load` publishes its `Runtime`; the Runtime
implements `agent.Loop` and adds only admission, lifetime cancellation and
in-flight drain. `Extension.Close` seals admission, cancels admitted calls and
waits for them to finish. A close deadline limits waiting only, so the owning
Set can retain dependencies and retry.

The Runtime cannot load or close itself. It does not own App, Session, IOA,
commands, history or protocols. Recursive and derived loop calls pass through
the same admitted Runtime. Product composition injects that capability into
scanners and the Session extension, and expresses lifetime ordering with
`extension.Entry.DependsOn`.
