---
name: parse_results
description: Use this skill to learn how to use the parse_results pseudo-command.
internal: true
---

# parse_results

Parse JSON-lines scanner output into structured analysis. Scanner must have been run with `-j` flag.

```bash
parse_results --scanner <scanner> --file <path> [--analysis <mode>]
parse_results --scanner <scanner> --data <inline_json> [--analysis <mode>]
parse_results <scanner> --file <path> [--analysis <mode>]
```

- `<scanner>` / `--scanner`: gogo, spray, or zombie.
- `--file <path>` / `--data <text>`: data source. Prefer `--file` for large outputs.
- `--analysis <mode>`: summary, targets, stats, or all (default: all).

```bash
parse_results --scanner gogo --file gogo_results.json --analysis summary
parse_results spray --file spray_output.json
```
