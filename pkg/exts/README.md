# Extensions

`pkg/exts` is the lifecycle adaptation boundary for product extensions. A package under
`tools/` or `agent/` implements behavior and remains unaware of host
lifecycle. An adapter in this package turns that behavior into an
`extension.Extension`; `core/extension.Set` then provides one publication and
shutdown contract.

Only initialization or resource cleanup needs an Extension. Pure declarations
are registered directly by the Profile; they need no tools/commands lifecycle
adapter. An Extension may contribute to several unrelated domains.
`pkg/exts/agent` owns admission, cancellation and draining for a selected
`agent.Loop`. `pkg/exts/session` installs the independent `agent/session`
manager and borrows that loop. Profiles can omit Agent
execution while keeping the remaining tools and commands.

Infrastructure extensions such as Harness and TUI own their domain registries.
Contributors borrow narrow registration interfaces, not concrete lifecycle
owners. Adapters must not create service locators or a second filesystem
implementation. Resource ownership and dependency order belong to the Set
entries assembled by the profile.

For example, `pkg/exts/proxy.Extension` owns the proxy Resource and publishes
its lifecycle-free Hub. Traffic protocol
handlers are stateless bindings registered directly on a connection-owned
`aop.NamespaceMux`; they are not a second Extension lifecycle.

An adapter never publishes its lifecycle owner. Files, Proxy, IOA, and App
construction separates a `Resource` from its named business object; only
Resource has Start/Open/Load/Close, while consumers receive Files, ProxyHub,
Runtime, or App directly. Agent follows the same boundary with one Extension
and its published Runtime; Session has its own separate installation.
There is no Borrow/Handle/sealed-interface layer. Extensions depend on business
capabilities rather than importing one another. Event producers, observers and
consumers share the profile's concrete `core/events.Stream`; it alone stamps
events through Publish, Observe and Consume.

IOA has two installations: `ioa/client` owns the complete client collaboration
capability, and `ioa/server` owns server storage and request draining. They do not
import each other. The client uses Commands, the canonical Event Stream and a
rejectable Inbox delivery callback. Static protocol skills are selected as a
`skills.Bundle` by the Profile. The server publishes only its business Server;
HTTP listeners remain owned by the command entrypoint.

The client owns its typed config, CLI declarations, console presentation, probes,
and collaboration skill assets. The server owns its independent CLI/config and
browser authentication bridge. Those adapters are inert contributions, not extra
lifecycle extensions. Generic hosts accept config Sections, CLI Actions, Console
Bindings, probe callbacks and HTTP Routes; none imports an IOA runtime. Product
compatibility mapping stays in `cmd/aiscan`. See [IOA composition](../../docs/ioa.md).
