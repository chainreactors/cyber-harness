# Structural Simplification

Prefer deletion over new abstractions. Review the producer, consumers, serialization tags, and tests before changing a type or forwarding function.

## Decision rule

- Remove a private type or function when it only copies fields or forwards one call, has no independent invariant, and its callers can use an existing type or function directly.
- Count the whole change: types, conversions, call sites, imports, branches, and tests. A smaller line count alone is not evidence of a simpler design.
- Keep a boundary when its two sides have different schemas, defaults, validation, ownership, lifetime, concurrency behavior, or external contracts. Examples include config versus runtime options, wire versus domain messages, raw responses versus cached content, and indexed database columns versus stored protobuf JSON.
- Keep adapters required by a dependency's callback signature or a public compatibility contract. A short implementation can still be necessary.
- Keep a small projection when passing the source type directly would expose unrelated fields or create an import dependency in the wrong direction.
- Do not replace deleted layers with generic helpers, aliases, configuration switches, or new DTOs unless they reduce the total concepts and transformations.
- If a helper is used only by tests and has no production contract, test the real path and remove the helper. Preserve behavior tests at the actual boundary.

## Review procedure

1. State what each candidate owns and identify every production caller, including build-tagged code and method values passed as callbacks.
2. Compare field types, tags, zero values, errors, side effects, and ordering before treating two structures as duplicates.
3. Make the smallest deletion that preserves those contracts. Prefer direct calls at the existing composition point.
4. Run focused tests for affected packages and build tags. Record any environment-only test blockers separately from regressions.
