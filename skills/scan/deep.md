# Deep Testing

Deep testing is aiscan's dynamic assessment skill. It runs after web endpoints or fingerprinted assets are discovered. Use it to decide which leads deserve deeper manual or browser-backed validation, not to follow a fixed vulnerability checklist.

## Assessment Standard

Use browser automation only when HTTP evidence is insufficient, such as JS-rendered content, client-side routing, dialogs, storage, network traces, or multi-step interactions. `playwright` is full-build only; call it only when it appears in the runtime pseudo-command list. Otherwise, use curl/fetch/manual HTTP evidence and mark browser-only checks `inconclusive` when they cannot be evaluated.

Prefer observation before mutation. When input testing is appropriate, use unique canaries. Pick follow-up targets based on observed attack surface, authentication boundaries, unusual fingerprints, parameterized endpoints, exposed debug/admin behavior, or user priorities.

Save concise reproducible evidence when deep testing confirms a loot. Close any browser session you open.

## Target-Feature Decision Tree

Choose the next branch from target traits and observed evidence. When several traits match, start with the branch that has the highest expected impact and freshest evidence. Do not run every branch as a checklist; switch only when the current branch stops producing useful evidence.

- Has login, accounts, tenant IDs, object IDs, or admin routes -> prioritize authorization and IDOR. Replace IDs across 3-5 observed or adjacent values and compare owner, non-owner, anonymous, and baseline responses when credentials are available.
- Is an API service, Swagger/OpenAPI surface, mobile-style JSON backend, or route set with many `/api/` paths -> prioritize unauthenticated access, method changes, role boundary checks, and feeding response fields from one endpoint into another.
- Has file upload, import, attachment, avatar, media, or document conversion -> prioritize upload validation, storage access, content rendering, metadata leakage, and post-upload authorization using benign canaries.
- Has search, filter, report, export, `sort`, `order`, or `orderBy` parameters -> prioritize injection-style validation and authorization-sensitive data slicing. Sorting parameters are high-value because they often reach query builders.
- Has GraphQL -> treat introspection as reconnaissance only. Report only if a query or mutation exposes protected data or performs an unauthorized action.
- Is a JS-heavy SPA with weak visible surface -> use katana or browser network/discover output as batch candidate sources, then inspect reachable bundles, source maps, route manifests, dynamic imports, embedded API clients, and network calls for hidden endpoints. Aim to exhaust reachable JS interface sources for the assessed scope; if time, auth, crawler limits, bundling, or blocked assets prevent that, state the limitation instead of claiming complete coverage.
- Has no obvious surface -> run katana-style endpoint discovery, mine JS/source maps/route manifests/robots/sitemap/archived routes, then group results by host/path/parameter shape before selecting high-value APIs. Do not feed every discovered URL back one by one.

## Efficiency Gates

- No PoC or executable reproduction means the result is not `confirmed`.
- P3/low/informational issues are normally not worth a standalone report unless the user explicitly asks for full inventory or they chain into real impact.
- CORS, security headers, version disclosure, GraphQL introspection, open redirect, and self-XSS are non-findings without a demonstrated impact chain.
- A confirmed reportable result must include a curl/protocol command, saved browser replay, or equivalent executable reproduction in the checkpoint content.
- If a branch produces no material progress after about 20 minutes or several negative probes, checkpoint the result and switch branches.

## Status Determination

- `confirmed`: probing produced direct, reproducible evidence of a security issue.
- `info`: useful attack surface or exposure was found, but not exploitable as tested.
- `not_confirmed`: the page was tested and no issue was supported.
- `inconclusive`: testing could not complete because of tool failure, timeout, unstable target, or contradictory evidence.

For fingerprinted non-web assets, do not invent browser activity. Use the supplied target and fingerprints to assess realistic exposure. A fingerprint alone is not a vulnerability: return `confirmed` only with direct evidence, `info` for meaningful exposure, `not_confirmed` when no issue is supported, and `inconclusive` when evidence is insufficient.

## Output Format

When finished, call the `checkpoint` tool:

- **kind**: "deep"
- **target**: tested URL
- **status**: confirmed, info, not_confirmed, or inconclusive
- **title**: one-sentence result
- **content**: concise markdown with exact commands and evidence
- **labels**: severity and classification tags when applicable

Call `checkpoint` exactly once. If a browser command fails or the session cannot complete, still call `checkpoint` with `status: "inconclusive"` and include the attempted commands and errors in `content`.

Do not output raw JSON. Always use the checkpoint tool to report your result.
