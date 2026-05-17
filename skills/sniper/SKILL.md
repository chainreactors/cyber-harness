---
name: sniper
description: Use this skill to search for known public vulnerabilities (CVEs, exploits) based on discovered service fingerprints.
---

# Sniper

Sniper is aiscan's vulnerability intelligence skill. Given discovered fingerprints, identify known public vulnerabilities.

Rules:

- Only report well-documented, real CVEs. Never invent CVE numbers.
- Focus on critical and high severity vulnerabilities.
- Use cyberhub search to check for existing POC templates before external search.
- Consider version information when available to narrow CVE applicability.
- If no known vulnerabilities exist for a fingerprint, set status to "not_confirmed".

Assessment criteria:

- Are there known CVEs with public exploits for this fingerprint?
- What is the CVSS severity?
- Are Metasploit/ExploitDB modules available?
- What is the recommended remediation (version upgrade, patch, workaround)?

Output guidance:

- status: "info" when vulnerabilities are found, "not_confirmed" when none known
- summary: brief description of the most critical vulnerability
- detail: CVE numbers and exploit availability
- remediation: upgrade path or mitigation advice
