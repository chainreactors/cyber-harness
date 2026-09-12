# Host workspace file extension

`NewWorkspace` constructs one `extension.Extension` owning the production workspace
tool registrations. It borrows the tool registry, optional virtual source, and
existing audit publisher. Load installs the tools atomically; Close revokes its
owner and waits for accepted calls.

This package preserves the host policy: absolute paths, invocation-specific work
directories, image rendering, and virtual readers. The bounded `filetools` extension
uses `files.FS` instead. Keeping these implementations in separate Go packages
prevents the minimal profile from acquiring audit/image dependencies through an
unused implementation. Production audit injection remains an outstanding
migration to filesystem observation; see the delivery record.

Both are installed by the same `core/extension.Set`; these are file policy choices,
not separate extension frameworks.
`workspacefiles` is the host-workspace tool extension. It owns registrations
whose paths may be absolute, invocation-relative, or supplied by a virtual
reader/glob source. It does not own an `os.Root` and does not replace
`pkg/files.FS`; bounded profiles use `pkg/toolset/filetools` instead.
