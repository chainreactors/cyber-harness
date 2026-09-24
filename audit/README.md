# cyber-audit

Model-led source and binary auditing on cyber-harness. The model
investigates code and business logic; tools provide text/AST search, dependency
advisories, content leak evidence and static binary analysis. No LSP or SAST engine
is required.

## Build

Go 1.26, from a checkout of this repository:

For a single executable containing the tools selected for your platform, run
from the repository root:

```sh
make audit ARSENAL_EMBED=1
```

This produces `bin/cyber-audit` (`.exe` on Windows). Downloads happen during the
build. On first use, audit extracts the tools before model startup, without
network access. The harness [arsenal.yaml](../tools/arsenal/arsenal.yaml)
owns tool definitions and default versions; [cmd/cyber-audit/bundle.yaml](cmd/cyber-audit/bundle.yaml)
selects tools by name, with platform additions for reverse analysis. Tool updates
require no CRTM change. Both runtime requirements and bundle metadata are generated
from that selection. All distributions use the `arsenal_embed` tag, with separate
payload directories so audit never includes aiscan's bundle.
GitHub release builds enable this mode and test the packaged Linux and Windows
executables in isolation, including Windows after UPX.

To generate a bundle and build manually, from the repository root:

```sh
go run github.com/chainreactors/crtm/cmd/crtm-bundle -config audit/cmd/cyber-audit/bundle.yaml -target linux/amd64 -output audit/internal/toolchain -package toolchain
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C audit build -tags arsenal_embed -o ../bin/cyber-audit-linux-amd64 ./cmd/cyber-audit
```

Run the generator on the host, with the same `-target` as the subsequent build.
For a smaller executable that downloads tools at runtime instead:

```sh
cd audit
GOWORK=off go mod download
GOWORK=off go build -o cyber-audit ./cmd/cyber-audit
```

After editing the YAML files, run `make audit-arsenal-spec` from the root before
a direct `go build`. `make audit` updates this metadata automatically, without
downloading tools. Generated metadata is committed so a clean checkout builds.

PowerShell: `$env:GOWORK = 'off'; go build -o cyber-audit.exe ./cmd/cyber-audit`.
The nested module is `github.com/chainreactors/cyber/audit`. Relative replacements
use the root harness and AOP modules. Build from the repository; independent
`go install ...@version` is not supported yet. Release builds include six binaries
(Windows/Linux/macOS, amd64/arm64). Cross-compilation uses `CGO_ENABLED=0`.

CRTM's installer and bundle generator are maintained in the upstream CRTM module.
The root and audit modules pin the same revision. For local CRTM development,
temporarily replace it with `../crtm` in the root module and `../../crtm` in audit,
then remove those replacements before publishing.

## Run

```sh
cyber-audit init
cyber-audit --workdir /path/to/repository
cyber-audit --workdir /path/to/repository -p "Audit authentication and tenant isolation"
cyber-audit --workdir /path/to/repository --task-file audit-task.txt
cyber-audit --workdir /path/to/repository --resume /path/to/report/session.jsonl
cyber-audit tools install
cyber-audit doctor
```

Reuse harness configuration/model commands and `--provider`, `--base-url`,
`--api-key`, `--model`, `--config`, `--data-dir`. No task starts the local REPL;
`-p`, `--task-file`, or `-i` starts a one-shot run. `--resume` restores recorded
history; without a new task it opens the REPL. `--timeout` and Ctrl-C cancel runs.
Use `--output-format json` or `stream-json` for machine-readable session output.

Project configuration and skills are discovered automatically. Audit takes model
settings from user configuration, environment variables or explicit CLI options;
it ignores the `llm` section of automatically discovered project files. Use
`--config path/to/cyber.yaml` to select a project model configuration explicitly.
`config show` follows the same rule. Project skills supplement the built-in audit
workflow, which is always part of the system prompt even when a project skill is
also named `audit`.

The repository may be any local checkout, including third-party repositories.
Clone with Git first if necessary. Git is optional for file auditing and required
for history/diff operations; audit does not install Git or modify system PATH.
Windows uses the harness shell (Git Bash recommended; cmd fallback is available).

## Required tools

| Capability | Tool | Pinned installation |
| --- | --- | --- |
| Text search, filtering, navigation hints | ripgrep (`rg`) | 15.2.0 |
| Structural patterns | `ast-grep` | 0.45.3 |
| Dependency advisories | `osv-scanner` | 2.6.0 |
| Content leaks | built-in `proton` | harness dependency and keys rules |
| Binary inspection and disassembly | `radare2` | 6.2.2; Windows amd64 |
| Executable capabilities | `capa` | 9.4.0; Windows/Linux amd64 |
| Static, stack and decoded strings | `floss` | 3.1.1; Windows/Linux amd64 |

Preflight and the session share one Arsenal manager and installation state.
Before provider startup, audit checks shared `arsenal/bin` and PATH, verifies
version and required CLI behavior, then installs missing/incompatible tools via
CRTM. Compatible versions are retained: same major and at least the pinned
version; for ast-grep 0.x, same minor. A broken shared binary is repaired because
that directory takes precedence in the child PATH. No latest-version check is
performed each launch. Installation validates a staged executable and preserves
an existing version on failure. `tools install` preprovisions without starting a
model. `doctor` only checks tools, never creates directories or downloads.

The three source tools support all six platforms. Reverse tools are included
only on the platforms listed above. Bundled builds prepare tools from embedded resources; smaller builds need GitHub access when a
required tool is missing. Bundle-managed installations follow application
versions, while user upgrades and edits are preserved. `doctor` remains read-only,
even in bundled builds. SCA needs OSV connectivity or an explicitly provisioned
offline database; embedding the executable does not include that database.
ast-grep Linux binaries may require glibc.

Third-party GitHub release tools remain available through
`arsenal add owner/repo --name NAME --pattern PATTERN`; advanced CRTM YAML entries
can define tag patterns and platform-specific assets/executables. This is not a
generic go/npm/pip/source installer. External CLIs execute through bash; audit
adds no duplicate search/scan wrappers.

## Binary analysis

Point `--workdir` at a directory containing the sample and describe the target
with `-p`. Call the available tools directly through bash:

```sh
radare2 -N -q -c ij sample.exe
radare2 -N -q -c 'aaa;aflj' sample.exe
radare2 -N -q -c 'aaa;s entry0;pdfj' sample.exe
capa -q -j sample.exe
floss -q -j sample.exe
```

Each tool is a single executable; capa includes its rules. This first stage adds
static inspection and disassembly, capability evidence and string extraction.
It does not include r2ghidra, Java/Android/.NET decompilation or firmware unpacking.
Host platform support does not imply support for every target format or CPU.
Record unsupported analysis and warnings in coverage; capability matches and
strings alone do not prove a vulnerability. Do not execute a sample by default.

## Evidence and limits

Each run creates `<workdir>/.cyber/audit/<run-id>/`, or a **new** `--report-dir`:
`run.json`, resumable `session.jsonl`, `coverage.json`, `findings.json`, `index.md`,
`log.md`, `raw/`, `evidence/`. Existing report directories are never overwritten.
The model records full scan outputs, stderr and exit status; the harness records
the canonical session. One-shot success requires coverage, SCA/leak check outcomes,
valid finding states/evidence paths, a final report and passing OKF validation.
An empty findings list or interrupted run is not a clean bill of health.
REPL and one-shot runs use the same report and OKF validation when closing.
An unfinished or invalid report is marked incomplete and returns a nonzero exit
status. JSON results are emitted after report finalization; stream-json includes
an AOP error event if finalization fails. The overall timeout includes tool
preparation, model startup and report validation.

File tools use repository-relative paths. A report directory outside the workdir
is writable through bash. Ripgrep receives a default exclusion config, proton
excludes the report/.cyber/.git paths, and audit instructions supply exclusions
for AST/directory dependency searches. Explicit CLI override flags can bypass
search defaults; these are scope conventions, not a security sandbox.

Finding states: candidate, confirmed, dismissed, inconclusive. Verification:
static, reproduced, not_attempted. The report distinguishes observed execution
from static reasoning. Text search covers any text language; AST and dependency
coverage depend on upstream parsers/ecosystems. Missing coverage stays explicit.
Proton scans current content with keys rules. It does not replace Gitleaks Git
history, attribution, baseline, decoding or allowlist behavior. Review matches and
redact secrets in findings; raw evidence can contain sensitive values.

## Validation

```sh
GOWORK=off go test ./...
GOWORK=off go vet ./...
```

CI tests this module separately on Windows, Linux and macOS, tests the CRTM
installer, enforces dependency boundaries, and cross-compiles six targets.
Integration tests use a local scripted provider to exercise real file/proton/OKF
tools, report completion, cancellation and JSONL resume. They test the runtime
contract, not the model's vulnerability-discovery quality.

Real-tool and live-model checks are opt-in; see [tests](tests/README.md).
