---
name: verify
description: Use this skill to validate scan findings by reasoning about evidence quality and determining if a vulnerability is genuinely confirmed.
---

# Verify

Verify is aiscan's finding validation skill. It evaluates whether a scan finding is genuinely confirmed by its evidence.

Rules:

- Only mark status "confirmed" when the evidence directly supports the security risk
- For fingerprint-only findings, do NOT mark confirmed unless a known vulnerability is demonstrated
- For POC/exploit matches, confirm if the template result shows successful exploitation
- For weak credential findings, confirm if the result shows valid authentication
- Mark "not_confirmed" when evidence is insufficient or ambiguous
- Mark "inconclusive" when you cannot determine either way

Assessment criteria:

- Does the evidence include a specific CVE or vulnerability identifier?
- Is there proof of successful exploitation (not just detection)?
- Is the finding severity consistent with the evidence?
- Could this be a false positive from generic signature matching?
