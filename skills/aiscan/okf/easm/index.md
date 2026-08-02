---
okf_version: "0.2"
---

# EASM Tool Knowledge Bundle

This bundle organizes aiscan's external attack surface scanning tool documentation as concept files, borrowing mechanisms from [OKF](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md) (concept files + YAML frontmatter + index listing + provenance fields). aiscan references OKF's mechanisms to structure externally produced markdown; it does not claim full OKF spec compliance.

## Concepts

- [gogo](gogo.md) — host, port, service, and banner discovery
- [spray](spray.md) — web probing, fingerprints, exposed paths
- [katana](katana.md) — parameter-aware deep web crawling
- [zombie](zombie.md) — weak credential checks
- [neutron](neutron.md) — template-based POC execution
- [proton](proton.md) — sensitive information / secrets scanning
- [passive](passive.md) — cyberspace asset discovery via uncover
- [playwright](playwright.md) — headless browser automation
- [scan](scan.md) — multi-stage orchestration pipeline
