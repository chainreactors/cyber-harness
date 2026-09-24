---
type: reference
---

# Minimal tools

`read`, `ls`, `glob` and `bash` provide code access. Use `write` for audit artifacts.
Always quote paths. Shell syntax depends on the runtime environment; on Windows
Git Bash is preferred. Consult each CLI's `--help` before unfamiliar options.

## Text and navigation

```sh
rg --files --hidden -g '!.git/**' -g '!.cyber/**'
rg --json --hidden -g '!.git/**' -g '!.cyber/**' -g '*.go' 'Authorize|CommandContext' .
rg -n -F 'FunctionName' src
```

Ripgrep's report exclusions are also set in RIPGREP_CONFIG_PATH. Honor the
additional report exclusion shown in the system prompt. `--no-config` bypasses
these defaults. rg exit 0 means matches, 1 means none, 2 means an error. Limit
large searches by path/language, then read surrounding code with line numbers.
This works on text in any language, without claiming semantic references.

## Structural filtering

```sh
ast-grep run --lang ts --pattern 'console.log($$$ARGS)' --json --globs '!.cyber/**' src
ast-grep run --lang go --pattern 'exec.Command($CMD, $$$ARGS)' --json --globs '!.cyber/**' .
```

Add the report exclusion from the system prompt with `--globs` when it is inside
the repository. Supported parsers vary by language. JSON coordinates are zero
based; convert to one based lines in reports. A parse failure is not a zero-match
result. Simplify an ambiguous pattern and validate it on a small positive example.
Some patterns are arity-sensitive: `exec.Command($CMD, $$$ARGS)` matches calls
with arguments after the command; separately inspect single-argument calls.
Fall back to rg/read for unsupported syntax or languages. ast-grep supplies AST
patterns, not whole-program data flow or call graphs. Search exit 1 means no match;
other nonzero results need diagnosis and must remain visible in coverage.

## Dependency evidence

```sh
osv-scanner scan source --recursive --format json --no-call-analysis=go --no-call-analysis=rust --output-file <report>/raw/osv.json .
osv-scanner scan source --lockfile <path> --format json --no-call-analysis=go --no-call-analysis=rust --output-file <report>/raw/osv.json
```

For directory scans add `--experimental-exclude .cyber` and an explicit report
directory exclusion. Disable call analysis as shown to avoid build-script execution.
Prefer explicit discovered lockfiles/manifests when the repository contains
report output or generated fixtures. `--help` lists supported inputs/options.
Version 2: exit 0 means a completed scan with no vulnerabilities; exit 1 means
vulnerabilities were found; exit 128 means no supported packages were found.
Other nonzero statuses are failures. Capture stderr and exit status even when
JSON exists. Do not auto-run dependency installers or remote build scripts.
Network/database failure means SCA incomplete. Cross-check ecosystem support,
multiple lockfiles, dev dependencies and actual reachable vulnerable code.

## Content leak evidence

```sh
proton -i . -c keys -j --no-stats -o <report>/raw/proton.jsonl
proton --template-list -c keys
```

Read [proton documentation](cyber://proton/proton.md). Results aggregate by rule
and path; inspect all events to preserve multiple occurrences. Report directories
and .git are excluded by the audit extension. No full Git-history scan or Gitleaks
parity is implied. Redact secret values in written findings and verify context.

## Git and tools

Git is supplied by the environment. Use `git status`, `git diff`, `git show` and
`git log` for repository context when available; report the limit if it is absent.
Do not print credential-bearing remote URLs. Tool versions are in run.json.
`arsenal add owner/repo --name NAME --pattern PATTERN` retains third-party GitHub
release support; this is not a general go/npm/pip/source build installer.
