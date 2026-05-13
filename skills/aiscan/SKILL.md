---
name: aiscan
description: Use this skill when the agent needs to understand aiscan mechanisms, available capabilities, scanner pseudo-commands, and tool invocation rules.
---

# Aiscan Mechanisms

Aiscan is an autonomous security scanning agent that orchestrates local tools and embedded ChainReactors scanner engines. Use deterministic scanner output as evidence, then reason about scope, retries, verification, and reporting.

Core tools:

- `read`: read workspace files and embedded skill files such as `aiscan://skills/aiscan/SKILL.md`.
- `write`: create or update requested report and evidence files.
- `glob`: discover local files in the current working directory.
- `bash`: run shell commands and scanner pseudo-commands.

Scanner pseudo-commands available through `bash`:

- `scan`: broad orchestration across discovery, web probing, weakpass, POC stages, priority tagging, and optional LLM verification.
- `gogo`: host, port, service, and banner discovery.
- `spray`: web probing, fingerprints, common files, crawl, and focused path checks. The aiscan wrapper adds `--no-bar` by default to keep agent output non-interactive.
- `zombie`: authorized weak credential checks for supported services.
- `neutron`: template-based POC checks when templates are available.

Operating rules:

1. Keep top-level aiscan flags separate from scanner flags. `aiscan -p` is the natural language prompt; inside scanner commands, `-p` keeps the scanner's native meaning, such as gogo ports or zombie password.
2. Prefer pseudo-commands over raw external scanner binaries so output is captured and bounded by the agent runtime.
3. Use non-interactive output. Avoid progress bars, terminal UI, and unbounded streaming unless a scanner integration explicitly supports safe streaming.
4. Use conservative thread counts and timeouts for localhost, fragile services, or narrow verification.
5. Record important evidence in files when the user asks for a report or reproduction.
6. Use `scan --verify=high` when the user asks to reproduce or validate risky findings. It enables `agent_verify`, which only handles high-priority findings by default.

Useful command forms:

```bash
scan -i <target> --mode quick
scan -i <target> --mode quick --verify=high
scan -i <target> --mode full --debug
gogo -i <ip-or-cidr> -p top100
spray -u <url> --finger
zombie -i <service-url> --top 3
neutron -i <url> --finger <finger-name>
```
