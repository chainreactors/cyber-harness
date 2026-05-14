---
name: web_search
description: Use this skill to learn how to use the web_search pseudo-command.
internal: true
---

# web_search

Search the web. Backend auto-selects: Tavily API when `TAVILY_API_KEY` is set, DuckDuckGo otherwise.

```bash
web_search <query> [--num <N>]
```

- `<query>`: positional, multi-word auto-concatenated.
- `--num <N>`: results count, 1–10, default 5.

```bash
web_search "CVE-2024-1234 exploit"
web_search nginx misconfiguration --num 10
```
