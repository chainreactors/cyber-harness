# Tool Registry

`Registry` is the profile-scoped publication and execution boundary for Agent
tools. Extensions contribute declarations during `Load`; the Registry activates
after every contributor and closes before them. It rejects new calls, cancels
accepted calls, and drains them before contributor resources are released.

Native pseudo-shell commands use `pkg/commands.Registry`. The two domains keep
their own execution contracts and share only `core/registry` lifecycle state.
