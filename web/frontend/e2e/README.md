# Web frontend tests

These tests exercise the Web UI, browser AOP client and Go application. Run them from `web/frontend/`. The checkout must include its submodules and the Go version declared in the root [go.mod](../../../go.mod).

## Prepare and run

Install Node.js/npm (CI uses Node.js 22), frontend dependencies and the Playwright browser:

```sh
cd web/frontend
npm ci
npx playwright install chromium
npm run build
```

The main test command runs the default and JEV suites. Select a narrower suite with its configuration:

```sh
npm run test:e2e
npx playwright test -c e2e/ui.config.ts
npm run test:jev
npx playwright test -c e2e/record.config.ts
```

The record suite requires the native recording build described in [record](../../../docs/record.md). Test configuration files and their server helpers determine each suite's prerequisites and environment variables.

## Session and transport boundaries

```sh
npx playwright test -c e2e/boundary.config.ts
```

The managed server uses isolated temporary configuration and databases, local and remote Go Agent paths, a controllable local Provider and Vite for browser-client tests. The Web UI project uses Go's embedded production frontend. Its network recovery tests use that frontend because Vite reloads the page after reconnecting. This suite needs no real Provider credential.

| Test file | Scope |
| --- | --- |
| `boundary.spec.ts` | Web UI, Go handlers, WebSocket and the fault Provider |
| `boundary-transport.spec.ts` | Browser AOP handshake, timeout and ownership behavior with a controlled socket and clock |
| `boundary-history.spec.ts` | API pagination with protobuf responses |
| `boundary-projection.spec.ts` | Event-derived run state, child turns, late frames and history replacement |
| `boundary-guardrail.spec.ts` | React hook and AOP client handling of controlled replies, expiry, visibility, reconnects and stale queries |

## Boundary server settings and artifacts

| Variable | Purpose | Default |
| --- | --- | --- |
| `CYBER_BOUNDARY_PORT` | Browser-client frontend port | `38479` |
| `CYBER_BOUNDARY_BACKEND_PORT` | Go server port | `38480` |
| `CYBER_BOUNDARY_PROVIDER_PORT` | Local fixture Provider port | `38481` |
| `BASE_URL` | Use an existing fixture instead of managing a server | Unset |
| `CYBER_BOUNDARY_GUI_URL` | Override the embedded frontend URL independently | Go server URL |

When using `BASE_URL`, start the fixture yourself and provide the matching embedded UI URL where necessary. Screenshots, traces and the HTML report are written to `.runlogs/rc7-boundary/` at the repository root, as configured in `boundary.config.ts`.

## Live Guardrail checks

The [Guardrail live suite](guardrail-live.spec.ts) needs a dedicated Web instance with JEV and an LLM configured on the server. It exercises real model-driven echo commands, human review, automatic assessment, history replay, mode changes and reconnection. It temporarily changes policy and mode, then restores them during teardown; profile reload cancels active work and pending reviews.

Set `BASE_URL`, `ACCESS_KEY`, `CYBER_E2E_NODE` and `CYBER_GUARDRAIL_LIVE_E2E=1`, then run from `web/frontend/`:

```sh
npx playwright test e2e/guardrail-live.spec.ts
```

Provider credentials stay on the server. The ordinary Go Guardrail tests use mock HTTP and dummy credentials. To exercise JEV directly, set `CYBER_JEV_LIVE_TEST=1` and `TYPESAFE_API_KEY`, then run from the repository root:

```sh
go test ./exts/guardrail -run TestLiveJEV -v -count=1
```

The direct test sends synthetic operation descriptions and does not execute the described tools. Guardrail configuration and approval commands are documented in the [tool guide](../../../docs/user/tools.md#工具准入与审批).

## Real Reflex execution evidence

First run the [real browser acceptance](../../../docs/jev.md) with actual LLM/JEV credentials. It runs the production Agent, compiler, native contracts and browser commands against isolated business applications. The private business oracle checks current random inputs, receipts and effect counts. No source or model answers are seeded.

Set `JEV_ACCEPTANCE_REPORT_DIR` to that report directory and `JEV_ACCEPTANCE_CASES` to the completed cases (`shadow,repeat,expense`), then run from `web/frontend/`:

```sh
npm run test:jev -- --grep 'real.*Reflex'
```

`jev-live-acceptance.spec.ts` requires correct business rows, a warm task with full Reflex takeover, zero main model tool calls and unchanged source. It replays the original AOP events and generated library with production components, checks dispatch/effect counts and qualification, and captures desktop/mobile screenshots. This verifies the UI against real execution evidence; it does not submit a task through the production Web frontend. Without a report directory, these opt-in tests skip. Reports and screenshots contain no credentials and stay outside the source commit.

## Related Go checks

Run from the repository root:

```sh
go test -tags "sqlite" ./agent/provider ./agent/session ./pkg/config ./pkg/node ./pkg/web/api ./pkg/web/service
go test -race -tags "sqlite" ./agent/provider ./agent/session ./exts/provider ./pkg/config ./pkg/node ./pkg/web/api ./pkg/web/service ./cmd/cyber-scan
```

The race check requires CGO and a C compiler; on Windows, select the compiler with `CC`. Repository process tests are documented in [cmd/harness](../../../cmd/harness/README.md).
