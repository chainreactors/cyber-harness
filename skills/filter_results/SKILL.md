---
name: filter_results
description: Use this skill to learn how to use the filter_results pseudo-command.
internal: true
---

# filter_results

Filter JSON-lines scanner output by field criteria. Scanner must have been run with `-j` flag.

```bash
filter_results --scanner <scanner> --file <path> --filter '{"key":"value"}' [--operator <op>] [--limit <N>]
filter_results <scanner> --file <path> --filter <key=value>[,...] [--operator <op>] [--limit <N>]
```

- `<scanner>` / `--scanner`: gogo, spray, or zombie.
- `--file <path>` / `--data <text>`: data source.
- `--filter <json>` or `--filter <k=v,...>`: field criteria.
- `--operator <op>`: contains (default), equals, not_contains, not_equals.
- `--limit <N>`: max results, default 50.

```bash
filter_results --scanner gogo --file results.json --filter '{"port":"80","protocol":"http"}'
filter_results spray --file spray.json --filter "status=200" --operator equals
```
