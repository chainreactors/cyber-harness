# Extensions

`pkg/exts` is the composition boundary for product plugins. A package under
`tools/` or `agent/` implements behavior and remains unaware of host
lifecycle. An adapter in this package turns that behavior into an
`extension.Extension`; `core/extension.Set` then provides one publication and
shutdown contract.

Only independently owned resources or registrations need an Extension.
`pkg/exts/agent` owns admission, cancellation and draining for a selected
`agent.Loop` and its sessions as one installation. Its Runtime is the single
business surface for loop and session operations. Profiles can omit Agent
execution while keeping the remaining tools and commands.

Adapters must not create registries, leases, service locators, or a second
filesystem implementation. Resource ownership and dependency order belong to
the Set entries assembled by the profile.

For example, `pkg/exts/proxy.Extension` owns the proxy Resource and publishes
its lifecycle-free Hub. Traffic protocol
handlers are stateless bindings registered directly on a connection-owned
`aop.NamespaceMux`; they are not a second Extension lifecycle.

An adapter never publishes its lifecycle owner. Files, Proxy, IOA, and App
construction separates a `Resource` from its named business object; only
Resource has Start/Open/Load/Close, while consumers receive Files, ProxyHub,
Runtime, or App directly. Agent follows the same boundary with one Extension
and its published Runtime; there is no separate Session extension.
There is no Borrow/Handle/sealed-interface layer. Extensions depend on business
capabilities rather than importing one another. Event producers, observers and
consumers share the profile's concrete `core/events.Stream`; it alone stamps
events through Publish, Observe and Consume.
