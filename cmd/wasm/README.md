# cmd/wasm — aiscan agent-core as js/wasm

RFC [#189](https://github.com/chainreactors/CyberHub/issues/189) 方案 A: run
aiscan's `pkg/agent` loop/evaluator as the *brain* inside a browser extension,
with the JS host owning every side effect. **One brain, shared with the CLI.**

> **要单一化的是"脑（harness）"，不是"手（浏览器操作）"。** The loop makes every
> decision; go-rod / playwright / os-exec never enter the wasm. Browser tools are
> registered as host-dispatched **stubs** (schema only) and executed by the host
> on the user's real logged-in tab via `chrome.scripting`.

```
   brain (this wasm)              seam (syscall/js, JSON)              hand (JS host)
 ┌────────────────────┐      ┌────────────────────────────┐     ┌────────────────────┐
 │ agent-core          │ LLM │ __aiscanLLM(reqJSON)        │ --> │ real provider      │
 │  loop / evaluator   │────▶│ __aiscanTool(name,args,ctx) │ --> │ chrome.scripting   │
 │  (agent.wasm)       │◀────│ onEvent(eventJSON)          │ <-- │ UI / progress      │
 └────────────────────┘      └────────────────────────────┘     └────────────────────┘
```

The host is CyberHub Copilot's `src/offscreen/wasm-agent.ts`
(`WasmAgentConversation`); this module's wire protocol matches it exactly, so the
built `agent.wasm` drops straight into `public/wasm/`.

## Status

- ✅ `pkg/agent` (+ evaluator, provider, commands) compiles `GOOS=js GOARCH=wasm`
  **as-is** — it imports `pkg/commands`, never `pkg/tools`. No strip needed.
- ✅ End-to-end verified two ways: `testdata/smoke.mjs` (24/24) against the built
  module, and the extension's own real-host test (`WasmAgentConversation` +
  this `agent.wasm` + the real `finish` tool + multi-turn).
- ✅ Size (standard Go, stripped `-s -w`): **~17 MB** / **~4.2 MB** gzipped —
  *under* the `finger.wasm` (29 MB) the extension already ships. TinyGo (further
  shrink) is a follow-up, not a blocker.

## Build

```sh
make agent-wasm        # -> dist/wasm/{agent.wasm, agent.wasm.gz, wasm_exec.js} + size
```

`wasm_exec.js` must come from the **same Go version** that built the module
(the target copies it from `$(go env GOROOT)/lib/wasm/wasm_exec.js`). Ship it as
`wasm_exec.agent.js` in the extension's `public/wasm/`.

## Smoke test

```sh
make agent-wasm
node cmd/wasm/testdata/smoke.mjs dist/wasm/agent.wasm dist/wasm/wasm_exec.js
```

## Wire protocol (matches wasm-agent.ts)

Host installs two function globals; the event sink is a per-run argument:

- `__aiscanLLM(reqJSON, ctxJSON) -> Promise<respJSON>` — `reqJSON` is an
  OpenAI-shaped `ChatCompletionRequest`; `ctxJSON` is the run context
  `{tabId,url,runId}` so the host can bind cancellation. Resolve a
  `ChatCompletionResponse` JSON string. The host owns provider choice, keys,
  caching and fallback.
- `__aiscanTool(name, argsJSON, ctxJSON) -> Promise<result>` — `ctxJSON` is the
  run's context blob, threaded verbatim. `result` is a JSON string
  `{ content, isError, terminate }` (a plain string or object is also accepted).

Exports (installed by `main`, gated on `aiscanAgentReady === true`):

- `runAgent(payloadJSON, onEvent) -> Promise<resultJSON>` — the name the host calls
- `aiscanRunAgent(...)` — alias of `runAgent`
- `aiscanCancelAgent(runId) -> bool` — aborts an in-flight run by `context.runId`

`onEvent(eventJSON)` receives each `agent.Event` verbatim (see
`pkg/agent/event_json.go`); the host's `mapEvent()` translates it.

### payload

```jsonc
{
  "task": "the new user message",
  "systemPrompt": "...",
  "model": "...",
  "maxTurns": 25,
  "messages": [ /* prior transcript to hydrate */ ],
  "tools": [ { "name": "...", "description": "...", "parameters": { /* JSON schema */ } } ],
  "context": { "tabId": 12, "url": "https://…", "runId": "r1" },
  // optional: "temperature", "maxParallelTools", "maxTokens", "tokenBudget",
  //           "eval": { "criteria": "…", "maxRounds": 3 }   // enables the Goal loop
}
```

### result

```jsonc
{ "output": "...", "messages": [...], "turns": 2, "stop": "completed" }
// stop ∈ completed | terminated | max_turns | error   (+ "usage", "error" when relevant)
```

A mid-flight failure still **resolves** with `error` + partial transcript; only a
malformed payload rejects. Transcript hydration is via `messages` in/out.

## Scope / not in this module

- **Host side** lives in CyberHubCopilot (`src/offscreen/`, `allTools()`, browser
  tools). This module is only the brain.
- **Session persistence** — `session.go`'s `os`/`filepath` compile under wasm but
  do nothing; the host hydrates via `messages`.
- **TinyGo** size pass.
