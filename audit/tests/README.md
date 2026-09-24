# Audit validation fixtures

The ordinary suite is offline and uses a local scripted provider. To exercise
installed CLIs, provision a temporary data directory, then run from audit/:

```sh
cyber-audit tools install --data-dir /tmp/audit-tools
AUDIT_TEST_DATA_DIR=/tmp/audit-tools go test ./internal/toolchain -run TestInstalled -v
AUDIT_TEST_DATA_DIR=/tmp/audit-tools AUDIT_NETWORK_TESTS=1 go test ./internal/toolchain -run TestInstalledOSVNetworkResults -v
```

Network tests query current OSV data; a future advisory can change the clean
fixture expectation. Network errors must not be interpreted as a clean scan.

## Single-file release

Build from the repository root with `make audit ARSENAL_EMBED=1`. Then run from
`audit/`, supplying an absolute path to the resulting executable:

```sh
AUDIT_SINGLEFILE_BINARY=/absolute/path/to/bin/cyber-audit go test ./internal/app -run '^TestSingleFileRelease$' -count=1 -v -timeout 3m
```

PowerShell:

```powershell
$env:AUDIT_SINGLEFILE_BINARY = (Resolve-Path ../bin/cyber-audit.exe).Path
go test ./internal/app -run '^TestSingleFileRelease$' -count=1 -v -timeout 3m
```

The test copies only the executable to a temporary directory, starts with empty
tool storage, removes external tools from PATH, and rejects/counts external HTTP
requests. It checks read-only doctor, extraction, idempotency, deletion recovery,
and first-run preparation before the model starts. A local scripted provider then
executes arsenal remove/install, ripgrep, ast-grep, OSV's CLI capability check,
proton and OKF through the real agent process and verifies the saved report.
It makes no paid model calls and does not claim to test offline advisory coverage.
The release workflow runs it against the actual Linux/amd64 artifact.

## Live model evaluation

Configure a model normally, then audit testdata/repository from a copied temporary
worktree (so reports do not pollute fixtures). Do not expose expected.json to the
model. Use a fixed provider/model and preserve session.jsonl, run.json and tool
versions so a reviewer can reproduce the evaluation.

```sh
CYBER_AUDIT_LIVE=1 AUDIT_TEST_DATA_DIR=/tmp/audit-tools go test ./internal/app -run TestLiveModelAudit -v -timeout 10m
```

Live runs retain their copied worktree and reports in
`$AUDIT_TEST_DATA_DIR/evaluations/model-*` for manual review.

The fixture contains a shell-injection path, a protected lookalike and a
cross-tenant authorization flaw. The evaluation checks finding locations and
outcomes, evidence-backed coverage and rejection of the protected lookalike.
Review traces and reproduction evidence manually as well: a keyword or finding
count alone is not proof of useful auditing. This opt-in test uses your configured
provider and can incur model usage. It is skipped by default in CI.
