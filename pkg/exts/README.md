# Extensions

`pkg/exts` is the composition boundary for product plugins. A package under
`tools/` or `agent/` implements behavior and remains unaware of host
lifecycle. An adapter in this package turns that behavior into an
`extension.Extension`; `core/extension.Set` then provides one publication and
shutdown contract.

Only independently owned resources or registrations need an Extension.
`pkg/exts/agent` owns admission, cancellation and draining for a selected
`agent.Loop`. Sessions use that Loop while each retains its own conversation
state. Profiles can omit Agent execution while keeping tools or session control.

Adapters must not create registries, leases, service locators, or a second
filesystem implementation. Resource ownership and dependency order belong to
the Set entries assembled by the profile.

For example, `pkg/exts/proxy.Extension` owns the proxy Resource and publishes
its lifecycle-free Hub. Traffic protocol
handlers are stateless bindings registered directly on a connection-owned
`aop.NamespaceMux`; they are not a second Extension lifecycle.

An adapter never publishes its lifecycle owner. Files, Proxy, IOA, App, and
Session construction separates a `Resource` from its named business object;
only Resource has Start/Open/Load/Close, while consumers receive Files,
ProxyHub, Runtime, App, or Manager directly. Agent follows the same boundary
with Extension and its published Loop Runtime.
There is no Borrow/Handle/sealed-interface layer. Extensions depend on business
capabilities rather than importing one another. Event producers and consumers
share the profile's concrete `core/events.Stream`; it alone stamps events.
