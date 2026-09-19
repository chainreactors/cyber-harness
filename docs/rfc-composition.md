# RFC: simplify composition boundaries and profile lifecycle

Tracking issue: https://github.com/chainreactors/cyber-harness/issues/137

## Problem

Cyber has one extension lifecycle, but its public shape currently suggests more:
`app.App`, `profile.Application`, `profile.Factory`, `host.Host`,
`session.Runtime`, and `runner` all appear near the top-level entry points.
The reference distribution also relies on package `init` functions to register
build-tagged extensions and its composition root is private to `cmd/aiscan`.

This makes the harness harder to embed than its extension model requires and
makes build membership depend on import side effects.

## Decision

1. Keep three dependency layers:
   - `core` and `agent` contain reusable mechanisms and the agent runtime.
   - `pkg/base`, `pkg/exts`, and `tools` contain reusable capability packs.
   - `pkg/aiscan` contains the reference distribution; `cmd/aiscan` is its CLI host.
2. Keep `pkg/base.New` plus `extension.New` as the generic composition entry.
   Publish `pkg/aiscan.New` for embedders that want the reference distribution.
3. Replace browser, record, and Web `init` registration with build-tag-selected,
   explicitly called functions.
4. Rename the shared non-lifecycle `app.App` to `app.State`, rename
   `profile.Application` to `profile.Profile`, and remove `Factory.Build` and
   reflection-based nil handling. Constructors are called directly.
5. Keep `session.Runtime`, `host.Host`, and `pkg/runner`: they respectively own
   agent sessions, one AOP connection, and CLI execution modes.

No compatibility aliases or deprecated wrappers will be retained. The change
does not alter CLI flags, YAML fields, or protobuf wire contracts.

## Public API

Generic custom distributions continue to compose directly:

```go
extensions, err := base.New(config)
extensions = append(extensions, custom...)
set, err := extension.New(extensions...)
err = set.Load(ctx)
defer set.Close(context.Background())
```

The reference distribution is constructed with:

```go
profile, err := aiscan.New(aiscan.Request{ /* resolved host inputs */ })
```

The shared host callback becomes a plain function with the signature
`func(profile.Request) (profile.Profile, error)`. `profile.Profile.State()`
returns `*app.State` after a successful load.

## Migration

- Replace `*app.App` with `*app.State` and `App()` with `State()`.
- Replace `profile.Application` with `profile.Profile`.
- Replace `profile.Factory.Build(request)` with direct constructor invocation.
- Build custom distributions from `base.New`, append extensions in explicit
  load order, and give the resulting slice to `extension.New`.

## Non-goals

- Splitting the Go module.
- Moving scan or SCO protobuf messages in this change.
- Adding a builder, extension registry, dependency DAG, or runtime plugin loader.
- Changing authentication, policy, CLI, configuration, or protocol behavior.

## Acceptance

- No production composition is registered through `init`.
- Standard, full, and supported record builds select optional extensions explicitly.
- The minimal agent remains free of scanner, proxy, IOA, browser, record, and Web dependencies.
- Composition, Web profile replacement, race, vet, and build checks pass.
- README, architecture, development, migration, and changelog documentation reflect the new API.
