# Cyber application metadata v1

IOA owns transport message IDs, sender identity, content and refs. Cyber owns task scheduling, sessions, interruptions, delegation and feedback. Application fields are stored directly in IOA meta, implemented in core/collaboration. There is no application namespace wrapper. No application field is added to IOA Ref or to the IOA core schema. Unrelated dynamic metadata keys remain opaque.

## Envelope

JSON example (inside an IOA message):

```json
{
  "meta": {
    "version": 1,
    "event": "inbox.route",
    "id": "stable-route-event",
    "source": {"node_id": "remote-producer", "session_id": "monitor"},
    "target": {"task_id": "release-review"},
    "agent": {"name": "auditor", "mode": "active"},
    "status": "routed"
  },
  "refs": {"messages": ["original-ioa-message-id"]}
}
```

The application fields are version, event, id, source, target, interrupt, chain, agent, status and call_id. An event field opts a record into this protocol. Its fields have fixed types and event-specific constraints; unknown versions/events, null top-level protocol fields and unknown address/agent fields are rejected. Other top-level meta keys are retained and ignored by this protocol. Optional address fields are node_id, task_id and session_id. Task identity survives execution restarts; session identity refers to one execution. Source defaults to the containing IOA sender. Target defaults to refs.nodes for peer sends, or to the containing sender for automatic local records. Forwarded source provenance explicitly supplies source.node_id because the reporter may differ from the original sender.

| Event | Meaning | Additional fields | Executable input |
| --- | --- | --- | --- |
| message | CLI/direct agent communication | interrupt | Yes |
| inbox.route | Router admitted input to a task/session | agent, status | No |
| subagent.delegate | SessionStart recorded delegation | agent, status, call_id | No |
| subagent.return | SessionEnd recorded completion/failure | agent, status, call_id | No |
| task.result | Durable task result sent to requester | status, chain | Yes, as feedback |

Lifecycle/result events require a stable id. Route/delegate require a target task or session. Return requires a source session. Only message can interrupt. Only task.result can carry chain, and it cannot carry delegation fields. Agent describes a delegate (name/type/mode/model); call_id correlates the parent's tool call. Content remains the readable task/evidence/result, rather than another copy of the envelope.

## References and identity

- refs.messages carries explicit generic message references. Router records reference the input, subagent returns reference the delegate, and task results reference the original request. It is never repurposed to hold task or session IDs.
- Envelope id is an application event identity stable across transport retries; IOA may assign a fresh message ID on every send. Task-result admission uses sender/source-session/id for idempotency. Ordinary peer messages retain transport-ID admission semantics.
- source/target express addresses and affiliation, not causal edges inferred between every pair of executions belonging to a task. Cross-node task/session IDs are separate identities.
- CLI invocation supplies source session and selected target. It owns all reserved protocol fields, so --meta cannot forge a lifecycle event, status, chain or source. Unrelated dynamic keys remain intact. Automatic Router and Session records use the same fields.
- Subscribers interpret direct meta.event records only. Records without event remain visible but are not executed. Invalid application data is reported and retained by IOA; replay advances past rejected records instead of permanently blocking later valid input.

## Ownership and delivery

CLI sends own application construction; IOA SDK owns transport serialization/persistence. CollaborationExtension maps the application envelope into the fixed mailbox.Metadata runtime boundary. Internal mailbox kinds and recovery fields are not public IOA metadata. Router records embed the typed envelope in Metadata.Record; the old Handoff DTO is removed.

Router input records and task results share the existing durable SQLite outbox. Receiving an IOA send confirmation means persisted, not consumed; completion is a separate fact. ACK loss may yield several transport attempts for one application event. Session delegate/return recording is a synchronous lifecycle hook: a transport failure is reported as a record error and is not covered by the Router outbox. No exactly-once external-effect guarantee is made.

Feedback is admitted to existing work or recorded; it cannot create tasks, return to a task already in chain, or exceed 16 hops. Durable admission, mailbox wait/yield, scheduler capacity and tmux cancellation remain separate runtime responsibilities. IOA records these paths; it does not become their scheduler.

## Graph projection

The generic cyber-ui IOA GraphPanel understands refs.messages and an optional application graph extension. The Cyber application parses this envelope in src/lib/ioa-harness.ts and composes HarnessGraph. Task and session entities use space/node/kind/id identity; source/target and execution affiliation are labeled separately from refs.messages. Restarts, out-of-order records and equal IDs on different nodes do not create guessed execution order. Retry attempts retain their transport identities and show the shared stable event ID. The graph is a derived view of persisted facts, not a second message bus or canonical state.

Selecting a message centers it at readable zoom, including after container resize. The graph controls retain the full overview; large histories are not restricted to an unreadable default fit.

## Breaking switch

This change has no old-field decoder, dual writes or compatibility DTO. Legacy kind, feedback_id, handoff_id, source_task_id, source_session_id, target_session_id and meta.subagent have no execution semantics. The former meta["cyber.harness"] object is not decoded. Publishers must emit direct v1 metadata.

The durable active-state schema is now 2. Schema 1 is explicitly refused; select a fresh --state-dir and preserve old state for audit. No old database is automatically removed or rewritten. Other IOA metadata is not migrated.

## Validation

- core/collaboration: all event round trips and strict rejection.
- exts/ioa/client: validation before admission, lifecycle records never execute, foreign metadata ignored, 300+ history replay with malformed record, cursor storage failure and backpressure.
- exts/inbox and cmd/aiscan: actual Router/Session/tmux, durable outbox, ACK-loss deduplication, feedback cycle prevention, CLI process restart.
- Five real-model complex scenarios in exts/inbox/complex_live_test.go: versioned reconciliation, capacity-constrained DAG, urgent allocation correction, inventory replay/backpressure, release attestation after ACK loss.
- Browser suites test application composition, restart identity, cross-node scoping, late arrival/retries, invalid/foreign metadata and full console reads from a real Hub when configured.
