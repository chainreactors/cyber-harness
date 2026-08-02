---
okf_version: "0.2"
---

# Runtime Tool Knowledge Bundle

This bundle organizes aiscan's runtime mechanism tool documentation as concept files, borrowing mechanisms from [OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md) (concept files + YAML frontmatter + index listing + provenance fields). aiscan references OKF's mechanisms to structure externally produced markdown; it does not claim full OKF spec compliance.

## Concepts

- [arsenal](arsenal.md) — security tool package manager
- [tmux](tmux.md) — PTY session manager
- [proxy](proxy.md) — proxy nodes and proxied execution
- [mitm](mitm.md) — intercepted HTTP(S) traffic capture and analysis
- [ioa space](ioa-space.md) — IOA space selection and discovery
- [ioa send](ioa-send.md) — IOA messages and checkpoints
- [ioa read](ioa-read.md) — IOA inbox and thread reading
- [search](search.md) — cyberhub fingerprint/POC search
- [fetch](fetch.md) — URL content retrieval and focused extraction
- [loop](loop.md) — recurring agent task scheduling
