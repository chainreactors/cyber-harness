# Independent tool registrations

This package is a standalone `tool.Executor` and `extension.Extension`. It is not
wired into a Command registry, Agent, Runtime, or product App. The Cairn
profile uses it directly, and any other profile can compose the same extension
with explicit construction and lifecycle ownership.

- `New()` constructs an inactive registry without publishing tools or creating
  workers. `Load(ctx)` enables it; that context does not own its lifetime.
- `Register(ownerID, tools...)` validates and publishes a whole group at once.
  Name or owner collisions fail without changing existing registrations.
- `ToolDefinitions()` returns cloned AOP definitions, in registration order.
  There is no API that returns the registered executable objects.
- `ExecuteTool(ctx, name, arguments)` preserves the caller's context values,
  including optional Invocation metadata. No Session or model configuration is
  required. A tool panic returns an error and releases its in-flight claim.
- `UnregisterOwner(ctx, ownerID)` stops discovery and admission, cancels calls,
  and waits for them. The ID and names stay reserved for this registry's lifetime.
- `Close(ctx)` stops all owners. A canceled wait does not reopen admission or
  pretend that accepted work finished. Retry with a fresh context to finish
  waiting. A closed registry cannot be loaded again.

An owner ID identifies cleanup ownership inside one composition, not an
authorization boundary. Callers supply valid, non-nil tool instances, retain
responsibility for tool concurrency, and do not mutate published tools. A tool
must not synchronously close its own owner or registry from `Execute`: shutdown
waits for that invocation. The registry cannot forcibly stop an uncooperative
tool and never closes resources borrowed by tools.

The owner extension must declare the registry as a `DependsOn` dependency and wait
for `UnregisterOwner` before closing its resources. `filetools` implements this
pattern with an actual directory handle. Separate registry instances share no
registrations or cancellation state.

The registry is the production execution path for profiles that choose it; it
does not manufacture hooks, transports, or model state.
