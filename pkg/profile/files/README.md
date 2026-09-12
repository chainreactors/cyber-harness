# Minimal file profile

This is the minimal file-only library composition. It replaces the former
pkg/profile/cairn package; Cairn's complete product composition remains separate.
The actual cmd/runner entrypoint now uses pkg/profile/workspace to select optional
audit journaling and skill mounts. Its default selection has the same file tools.

```text
file tools → tool registry
file tools → filesystem
```

New constructs those concrete objects and their extension.Set. Load opens the root
and installs tools. Executor returns `(tool.Executor, error)` only after complete
loading and before closing. Close revokes tool admission before releasing the
root; retained Executor references still obey registry admission checks.

New accepts the filesystem's own files.Config directly: Directory is absolute;
ReadOnly omits write; MaxBytes defaults to 1 MiB. No profile configuration copy
or field-renaming layer is involved. Tools read/write accept relative paths and
UTF-8 text under the root.

Build and run from the repository root with GOWORK=off:

```powershell
go build -o artifacts/files-runner.exe ./cmd/runner
./artifacts/files-runner.exe --server http://127.0.0.1:8080 --workdir C:\workspace --read-only --json
```

The operator-supplied server must implement the existing AOP tool-node protocol.
The profile does not create it. Integration tests start a local server and
exercise the actual runner without external services:

```powershell
go test -race -timeout 60s ./pkg/files ./pkg/toolset/filetools ./pkg/profile/files ./cmd/runner
```
