---
name: scan
description: Use this skill when working with scan for the multi-stage aiscan pipeline across discovery, web probing, weak credentials, POC checks, and verification.
internal: true
---

# Scan

Scan is the multi-stage orchestration pipeline in aiscan.

Capabilities:

- combine discovery, web probing, weak credential checks, POC execution, and optional AI verification
- produce discovered targets, services, web endpoints, fingerprints, weak credentials, POC matches, errors, and final stats
- expose capability names such as gogo portscan, spray web probing, zombie weakpass, neutron POC, and agent verification
- run quick or full profiles depending on depth needs

Common usage:

```bash
scan -i <target> --mode quick
scan -i <target> --mode full
scan -i <target> --ai
scan -i <target> --sniper
scan -i <target> --mode full --port top1000
scan -i <target> -j
```

Notes:

- `quick` uses gogo `-p all -v`; `full` uses gogo `-p -` and adds spray default-dictionary probing.
- Spray web capabilities run with recon enabled in both profiles.
- `--ai` enables all AI skills: verify (validate findings via LLM) + sniper (search public CVEs for fingerprints).
- `--sniper` can be used standalone to only enable fingerprint vulnerability intelligence.
- `scan --verify=<level>` is deprecated; use `--ai` instead.
- User intent decides whether scan output should be summarized, analyzed, validated, reported, or used to choose follow-up commands.
