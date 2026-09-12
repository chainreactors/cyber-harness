# File tool registration extension

`filetools.New(registry, filesystem, owner)` constructs the bounded-file tool
extension. It owns
tool registrations and borrows `*files.FS` and Registry. Load requires both
dependencies to be active. Close unregisters and waits for calls without closing
either dependency.

The catalog is read/write/ls/glob; read-only FS omits write. Old read_file and
write_file names and filetools.Config are removed. Example arguments:

```json
{"path":"note.txt"}
```

```json
{"path":"note.txt","content":"hello"}
```

Tools parse parameters, enforce UTF-8 text and render the existing result. FS
owns root containment, limits, read-only policy and actual IO. Missing/null content
without edits is rejected; empty content writes an empty file. Targeted `edits`
use `old_text`, `new_text`, and optional `replace_all`, matched against original
content with overlap rejection and a final file size bound. Reads support optional
line offset/limit and explicitly mounted URI paths. Glob uses path.Match syntax;
recursive `**` is rejected. Traversal is bounded at 20,000 directory entries.

`pkg/profile/files` assembles Registry, FS and this extension as three entries in one
Set, declaring both dependencies on the tools. `cmd/runner` uses
`pkg/profile/workspace` for optional audit and skills selection. It must not be
used for host-workspace path policy; that belongs to `pkg/toolset/workspacefiles`.
No forwarding executor or compatibility package is used.

Host-workspace tools with absolute paths, image rendering and invocation-specific
directories are in `pkg/toolset/workspacefiles`. Keeping them in a separate Go
package preserves this bounded extension's dependency closure. Their audit and path
behavior remains a separate implementation; a shared API name is not evidence
that those policies have been unified.

Tests cover actual contents, rejected writes, path boundaries, failed registration
ownership and FS survival after tool removal. `cmd/runner/wire_test.go` exercises
the actual runner entry over a local AOP WebSocket.
