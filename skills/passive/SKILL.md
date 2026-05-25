---
name: passive
description: Use passive to expand domains/ICPs into cyberspace assets like IPs, URLs, ports via uncover (FOFA, Hunter, Shodan, Censys, etc.). Run before active scanners (gogo, spray, katana).
---

# Passive

`passive` is an aiscan command backed by `uncover` for cyberspace search. Pick a source with `-s`.

## Sources

### Cyberspace Recon (uncover)

| Source       | Provider    | Credential                                            |
| ------------ | ----------- | ----------------------------------------------------- |
| `fofa`       | FOFA        | `recon.fofa_email` + `recon.fofa_key` or env vars     |
| `hunter`     | Hunter      | `recon.hunter_api_key` or env `HUNTER_API_KEY`         |
| `shodan`     | Shodan      | env `SHODAN_API_KEY`                                   |
| `shodan-idb` | Shodan IDB  | none                                                   |
| `censys`     | Censys      | env `CENSYS_API_TOKEN` + `CENSYS_ORGANIZATION_ID`      |
| `quake`      | Quake       | env `QUAKE_TOKEN`                                      |
| `zoomeye`    | ZoomEye     | env `ZOOMEYE_API_KEY`                                  |
| `netlas`     | Netlas      | env `NETLAS_API_KEY`                                   |
| `criminalip` | CriminalIP  | env `CRIMINALIP_API_KEY`                               |
| `publicwww`  | PublicWWW   | env `PUBLICWWW_API_KEY`                                |
| `hunterhow`  | HunterHow   | env `HUNTERHOW_API_KEY`                                |
| `binaryedge` | BinaryEdge  | env `BINARYEDGE_API_KEY`                               |
| `onyphe`     | Onyphe      | env `ONYPHE_API_KEY`                                   |
| `driftnet`   | Driftnet    | env `DRIFTNET_API_KEY`                                 |
| `greynoise`  | GreyNoise   | env `GREYNOISE_API_KEY`                                |

Sources without credentials are silently skipped at init.
Additional sources can be configured via `~/.uncover-config/provider-config.yaml`.

## When to Use

- **Domain/ICP → IPs, URLs, ports**: use a cyberspace source (`fofa`, `hunter`, `shodan`, etc.)
- Feed output into `gogo`, `spray`, `katana`, or `neutron` for active scanning.

Skip `passive` when the user already provided concrete IPs or private ranges.

## Usage

```bash
passive -s fofa 'domain="example.com"'
passive -s fofa 'icp="浙ICP备16020926号"'
passive -s hunter 'domain.suffix="example.com"'
passive -s shodan 'org:"Example"'
```

The positional argument is the source-native query string.

## Output

### FOFA

JSON array with rich fields:

```json
[{"ip":"1.2.3.4","port":"443","url":"https://example.com","domain":"example.com","title":"Example","icp":"..."}]
```

### Hunter

JSON array with extra fields: `status`, `company`, `frame`:

```json
[{"ip":"1.2.3.4","port":"443","url":"https://example.com","domain":"example.com","status":"200","company":"Example Inc","frame":"nginx","title":"Example","icp":"..."}]
```

### Other sources (shodan, censys, etc.)

Generic JSON array:

```json
[{"ip":"1.2.3.4","port":"80","url":"http://example.com","host":"example.com","source":"shodan"}]
```

## Typical Pipeline

1. `passive -s fofa 'icp="京ICP备xxx号"'` → get IPs/URLs
2. `gogo` / `spray` / `katana` → active scan discovered assets

## Notes

- Hunter blocks overseas IPs; use `recon.proxy=socks5://...` for Hunter from abroad.
- ICP data may lag reality; treat domain mapping as leads, not authoritative.
