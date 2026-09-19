---
type: Tool Playbook
title: ioa finding
description: Publish vulnerability discoveries to the current IOA space as natural-language checkpoints, making them observable and reviewable by other nodes.
tags: [runtime, collaboration, ioa, finding]
status: stable
---

# ioa finding — Publishing Vulnerability Discoveries

When a scan produces a vulnerability worth acting on, publish it to the current
IOA space as a `checkpoint` message. IOA persists the message, any node can
watch the stream with `ioa read --listen`, and the checkpoint status forms a
review trail.

Requires an IOA-bound session (a space already selected via
[ioa space](ioa-space.md) or configured by the runner). If `ioa send` reports
"no space joined", skip publishing — findings still live in the local report.

## When to publish

Your judgment. Publish:

- **Verified vulnerabilities** — active probing confirmed the issue.
- **Verified weak credentials** — confirmed login on a real service.
- **High-value leads needing human review** — you cannot complete verification
  but the evidence is strong enough that a human should look.

Do NOT publish:

- Fingerprints, service banners, routine web artifacts.
- Sniper/CVE intelligence without exploitation evidence.
- Leads you already dismissed (strikethrough / not confirmed).

## How to publish

```
ioa send checkpoint --kind finding \
  --title "<one-line conclusion>" \
  --target "<host or url>" \
  --status pending \
  --content "<natural-language body>"
```

- `--status pending` — verified or high-severity, asks for human review.
- `--status info` — intelligence-level, no review requested.

Write the content natural-language first: what it is, where, how you verified
it, and the impact. End with a reference line linking the durable records:

```
result_id: <artifact result id> · evidence: mitm://flows/17, neutron PoC shiro-rce.yaml
```

The `result_id` is the finding's only identity — content-addressed from the
artifact data, so the same vulnerability re-scanned yields the same id and
anyone holding the artifact can recompute it. The full record lives at
`findings/<result_id>.md` in the local report bundle (see the report
reference); the IOA message is the observable notification.

## Review loop

A human or peer node reviews the checkpoint and replies with another
`ioa send checkpoint --kind finding` on the same target carrying
`--status confirmed` or `--status dismissed`. Treat that status as the
disposition record; mirror it into the local `findings/<result_id>.md`
frontmatter when you next touch the report.

## Related concepts

- Send mechanics and flags: [ioa send](ioa-send.md)
- Watching the stream: [ioa read](ioa-read.md)
- Space selection: [ioa space](ioa-space.md)
