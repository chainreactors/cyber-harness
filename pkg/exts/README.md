# Extensions

`pkg/exts` is the composition boundary for product plugins. A package under
`tools/` or `agent/` implements behavior and remains unaware of host
lifecycle. An adapter in this package turns that behavior into an
`extension.Extension`; `core/extension.Set` then provides one publication and
shutdown contract.

Adapters must not create registries, leases, service locators, or a second
filesystem implementation. Resource ownership and dependency order belong to
the Set entries assembled by the profile.
