# Traffic subscriptions and local storage

Traffic observation uses the existing typed eventbus and existing Flow model.
There is no additional transport DTO or subscription wrapper. A stream handler
and the metadata journal subscribe to the same store-owned bus; body files are
written by bounded byte-chunk subscriptions using the same eventbus mechanism.

The MITM engine owns completion: the existing Addon interface includes
`FlowFinished`, called once by `Flow.finish()` with terminal Error/EndTime.
Capture uses native `Flow.Stream` and `io.TeeReader`; it no longer infers
completion independently from Response, SSEEnd, error callbacks or reader EOF.
Explicit raw streaming bypasses buffered bodies and parsed SSE history.

## Local policy

The existing `cyberhub.mitm` boolean still controls interception/recording.
Local persistence is independent and cannot be enabled by remote Configure:

```yaml
cyberhub:
  mitm: true
traffic:
  body_storage: none           # default; use disk to retain bodies locally
  body_max_bytes: 8388608      # per request or response; 0 selects this default
  body_retention_bytes: 2147483648 # retained files; 0 selects this default
```

CLI equivalents are `--mitm-body-storage`, `--mitm-body-max-bytes`, and
`--mitm-body-retention-bytes`. Invalid modes, negative limits, and a retention
budget smaller than two maximum bodies are rejected before application startup.

With `none`, only metadata and at most 4 KiB of preview per body remain in
memory; no capture directory or traffic journal is opened. Larger bodies are
explicitly marked incomplete with a truncation notice. Existing files from an
earlier disk-enabled run are not read or deleted in none mode.

With `disk`, body data is incrementally recorded under
`.aiscan/mitm/capture/body`. The metadata-only `flows.jsonl` journal contains
body references, never body bytes or previews. Headers/URLs can still be
sensitive: metadata recording is not redaction. Flow publication waits for
file finalization, off the proxy reader path; no reference names an unfinished
final file. HTTP forwarding is unaffected by capture failure.

## Native subscription API

`Bus.Subscribe` remains synchronous. `SubscribeFiltered` adds selection while
preserving immediate visibility. `SubscribeAsync` returns the native
`Subscription[T]` and adds filtering, ownership cloning, count/byte admission
limits, ordered processing, error reporting, cancellation and draining.
Filters/size/clone functions must be fast and non-reentrant; all other work
belongs in the serial handler.

`Hub.SubscribeFlows(after, options, handler)` returns
`*eventbus.Subscription[Flow]` directly. The handler receives owned metadata,
and chooses whether to serialize it, write it, or hydrate the retained body.
Negative `after` means live-only; otherwise retained entries after the cursor
are replayed atomically with live registration. A cursor older than the retained
window cannot recover evicted history; replay exceeding the subscription's
budget fails explicitly. Old channel APIs remain adapters for compatibility.

Stream and metadata-journal defaults are 256 pending events and 4 MiB per
consumer, including the executing event. File recorders admit at most 16 bodies
per store, each with a 1 MiB byte queue; chunks are copied in at most 32 KiB
pieces. Slots remain held through finalization/publication, bounding outstanding
workers even if cleanup is slow. Active files can temporarily add at most
16 times the per-body cap beyond the completed-file retention budget.

Overflow stops only that subscriber, drops its queued work, and exposes
`ErrOverflow`. Body failures appear on their Flow; journal failures appear in
`IndexError` and traffic State; stream failures are reported through State.
`Cancel` discards pending work; `Close(ctx)` stops admission and drains.
`Stopped` closes immediately; `Done` closes after the executing handler and
callbacks return. A bus cannot interrupt a blocked syscall or arbitrary send
function: network transports must retain their own connection cancellation.

## Compatibility boundaries

The wire protocol still sends whole Flow messages, not body chunks. Disk mode
loads one retained body per active sender on demand; it does not queue hydrated
bodies. Thus this is bounded capture/streaming, not zero-memory full-body
transport. None mode sends previews only. Missing/evicted files are reported
as incomplete instead of silently looking like complete bodies.

The session AOP JSONL recorder keeps synchronous Emit/Switch visibility and
adds optional filtering. It is not merged with traffic metadata or assigned
synthetic session IDs. Moving that journal to async delivery, or adding a
chunked wire protocol, requires an explicit compatibility change.
