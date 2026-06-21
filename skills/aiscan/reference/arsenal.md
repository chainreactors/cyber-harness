
# arsenal

Security tool package manager. Search, install, update, remove CLI tools from chainreactors, projectdiscovery, and any GitHub repo.

Installed tools are immediately available via bash. `arsenal` itself runs through bash.

## First step

Before installing anything, run `arsenal list` to see what's available and what's already installed:

```bash
arsenal list
```

Output shows `*` and version for installed tools:
```
* v2.14.1  gogo               [chainreactors     ] Host, port, service and banner discovery
* v1.2.3   dnsx               [projectdiscovery  ] Fast DNS toolkit
           nuclei             [projectdiscovery  ] Fast vulnerability scanner with 9000+ templates
           httpx              [projectdiscovery  ] Fast HTTP toolkit with tech detection
3/22 installed
```

## Quick reference

```bash
arsenal list                                        # all tools + install status + version
arsenal search port scanner                         # find tools by keyword/tag
arsenal info nuclei                                 # detail + docs URL + hint + latest version
arsenal install httpx                               # install latest (idempotent)
arsenal install dnsx --version 1.2.3                # install pinned version
arsenal update nuclei                               # re-download latest version
arsenal remove httpx                                # delete installed binary
arsenal releases nuclei                             # check latest release tag
arsenal add ffuf/ffuf --pattern "{name}_{version}_{os}_{arch}.tar.gz"  # register third-party repo
```

## Key behaviors

- **install is idempotent** — already-installed tools return success, not error. Use `arsenal update` to refresh.
- **version from git tag** — versions come from GitHub release tags, not filename parsing.
- **install output includes docs + hint** — follow the hint (e.g. "run nuclei -update-templates").
- **gogo/spray/zombie are built-in pseudo-commands** — no install needed for those. Arsenal has them for standalone binary use only.

## Typical workflow

```bash
arsenal list                          # see what's installed and available
arsenal search subdomain              # find tools for the task
arsenal install subfinder             # install
subfinder -d target.com              # use directly
```

## Adding unknown tools

```bash
arsenal add ffuf/ffuf --pattern "{name}_{version}_{os}_{arch}.tar.gz"
arsenal install ffuf
ffuf -u https://target.com/FUZZ -w wordlist.txt
```

Common patterns: `{name}_{version}_{os}_{arch}.tar.gz` | `{name}_{version}_{os}_{arch}.zip` | `{name}_{os}_{arch}` (raw binary).
