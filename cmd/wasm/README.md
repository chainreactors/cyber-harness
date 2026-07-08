# cmd/wasm — aiscan agent-core as js/wasm

The **承重 spike (GATE)** for RFC [#189](https://github.com/chainreactors/CyberHub/issues/189)
方案 A: run aiscan's `pkg/agent` loop/evaluator as the *brain* inside a browser
extension, with the JS host owning every side effect.

> **要单一化的是"脑（harness）"，不是"手（浏览器操作）"。** The loop makes every
> decision; go-rod / playwright / os-exec never enter the wasm. Browser tools are
> registered as host-dispatched **stubs** (schema only) and executed by the host
> on the user's real logged-in tab via `chrome.scripting`.

```
   brain (this wasm)            seam (syscall/js, JSON)          hand (JS host)
 ┌────────────────────┐      ┌──────────────────────────┐     ┌────────────────────┐
 │ agent-core          │ LLM │ __aiscanLLM(reqJSON)      │ --> │ real provider      │
 │  loop / evaluator   │────▶│ __aiscanTool(name, args)  │ --> │ chrome.scripting   │
 │  (agent.wasm)       │◀────│ onEvent(eventJSON)        │ <-- │ UI / progress      │
 └────────────────────┘      └──────────────────────────┘     └────────────────────┘
```

## GATE result (this spike)

| check | result |
|---|---|
| `pkg/agent` (+ evaluator, provider, commands) compiles `GOOS=js GOARCH=wasm` | ✅ **as-is, no strip needed** — it imports `pkg/commands`, never `pkg/tools` |
| runs end-to-end (LLM ↔ tool ↔ event ↔ cancel) | ✅ `testdata/smoke.mjs` — 21/21 |
| size, stripped (`-s -w`) | **~17 MB** |
| size, stripped + `gzip -9` | **~4.2 MB** |
| baseline: `finger.wasm` already shipping in the extension | 29 MB |

Standard Go wasm already lands **under** the fingerprint wasm the extension ships
today, so "体积可接受" holds without TinyGo. TinyGo (to shrink further) is a
follow-up, not a blocker — its `encoding/json` reflection is the known snag.

## Build

```sh
make agent-wasm        # -> dist/wasm/{agent.wasm, agent.wasm.gz, wasm_exec.js} + size
```

Needs the matching `wasm_exec.js` (copied by the target from
`$(go env GOROOT)/lib/wasm/wasm_exec.js`) — it must come from the **same Go
version** that built the module.

## Smoke test

```sh
make agent-wasm
node cmd/wasm/testdata/smoke.mjs dist/wasm/agent.wasm dist/wasm/wasm_exec.js
```

## Wire protocol

The host installs two function globals and passes a per-run event callback:

- `__aiscanLLM(reqJSON) -> Promise<respJSON>` — `reqJSON` is an OpenAI-shaped
  `ChatCompletionRequest`; resolve a `ChatCompletionResponse` JSON string. The
  host owns provider choice, keys, caching and fallback.
- `__aiscanTool(name, argsJSON) -> Promise<result>` — `result` is either a
  string (result text) or `{ text, is_error, terminate }`.

Exports (installed by `main`, gated on `aiscanAgentReady === true`):

- `aiscanRunAgent(payloadJSON, onEvent?) -> Promise<resultJSON>`
- `aiscanCancelAgent(runId) -> bool` — aborts an in-flight run by `run_id`.

`onEvent(eventJSON)` receives each `agent.Event` (see `pkg/agent/event_json.go`).

### payload

```jsonc
{
  "run_id": "abc",                 // key for aiscanCancelAgent
  "prompt": "task...",
  "system_prompt": "...",
  "model": "...",
  "messages": [ /* prior transcript to hydrate */ ],
  "tools": [ { "name": "...", "description": "...", "parameters": { /* JSON schema */ } } ],
  "max_turns": 20,
  "max_parallel_tools": 4,
  "max_tokens": 0,
  "temperature": 0.0,
  "token_budget": 0,
  "eval": { "criteria": "acceptance criteria", "max_rounds": 3 }  // omit for plain loop
}
```

### result

```jsonc
{ "output": "...", "messages": [...], "turns": 2, "stop": "completed",
  "usage": { "total_tokens": 41, ... }, "error": "" }
```

A mid-flight failure still **resolves** with `error` + partial transcript; only a
malformed payload rejects.

## Scope / not in this spike

- **Plugin host side** (`agent-manager.js`, tool dispatcher, `buildDomTree.js`
  perception, security guardrails) — CyberHubCopilot repo, next step.
- **Session persistence host-out** — `session.go`'s `os`/`filepath` compile under
  wasm but do nothing; the host hydrates via `messages` in/out instead.
- **TinyGo** size pass.
