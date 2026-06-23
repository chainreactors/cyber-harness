---
name: proton
description: Use this skill when working with proton for sensitive information scanning — detecting API keys, tokens, credentials, and secrets in files or piped data.
internal: true
---

# Proton

Proton is the sensitive information scanner in aiscan. It detects API keys, tokens, credentials, private keys, database connection strings, and other secrets using template-based pattern matching (nuclei-style).

## Scan files or directories

```bash
proton -i /path/to/project
proton -i . -c keys,spray
proton -i /etc --severity high
```

## Pipe from other commands

Proton accepts piped input from any shell command or pseudo-command:

```bash
curl -s http://target/api/config | proton
cat .env.production | proton
spray -u http://target | proton --tags spray
```

## Template filtering (nuclei-style)

```bash
proton -i . --tags cloud                    # only cloud provider rules
proton -i . --id aws-access-key             # specific rule
proton -i . --exclude-id ip-with-port       # skip a rule
proton -i . -s high --exclude-severity info # severity filter
proton --template-list -c keys              # list available rules
```

## Custom regex expressions

```bash
proton -i . -e "AKIA[0-9A-Z]{16}"
proton -i . -e "password\s*[:=]" -e "secret\s*[:=]"
proton -i . -e "custom_token_[a-z0-9]{32}" --ext .go,.py
```

## Custom templates

```bash
proton -i . -t ./custom-rules
proton -i . -t ./rules -c keys     # merge with builtin
```

## Output

```bash
proton -i . -j                     # JSON Lines
proton -i . -o findings.txt        # save to file
proton -i . -j -o findings.jsonl   # JSON to file
proton -i . --silent               # findings only, no stats
```

## Multi-target from file

```bash
proton -l paths.txt -c keys
proton -l targets.txt --severity high -j
```

## Notes

- 156+ builtin rules from found/keys and found/spray template categories.
- Templates use the same YAML format as neutron (proton template protocol).
- Severity levels: critical, high, medium, low, info.
- A finding is pattern evidence; user intent decides whether to triage, verify, or report it.
