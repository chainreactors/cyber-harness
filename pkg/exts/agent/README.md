# Agent lifecycle

`New(loop)` constructs an inert Extension, the sole lifecycle owner of its
Runtime. Only `Extension.Load` and `Extension.Close` control that lifetime.
`Extension.Loop()` lends a Runtime implementing the existing `agent.Loop`
interface, without exposing Load or Close. Its `Run` combines caller cancellation
with the installed lifetime; Extension.Close rejects new calls, cancels accepted calls
and waits for their actual completion. A close timeout can be retried through
the owning Set, which keeps dependencies alive while calls drain.

The profile supplies the algorithm and its dependency entries. The Extension
does not create another Agent, Session, tool registry or event stream. Session
history and per-Agent concurrency protection stay with their existing owners.
Derived Agent configs retain the installed Loop, so their execution uses the
same lifetime. Cancellation of one call does not close other sessions.

AIScan installs this Entry when a session Loop or scanner AI is selected. A
session configuration with a nil Loop keeps reasoning disabled; the minimal
file profile installs neither this Extension nor a Session Manager. The Loop
selection is fixed by composition and is not overridden by individual sessions.

Custom Go compositions pass `Extension.Loop()` anywhere an `agent.Loop` is
accepted and put the Extension after its resources in the same `extension.Set`. The raw
Loop remains usable without the lifecycle plugin when its caller owns execution.

```text
go test -race ./pkg/exts/agent ./pkg/profile ./pkg/exts/session ./cmd/aiscan
```
