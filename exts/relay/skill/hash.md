# Hash lookup — `relay lookup-hash <hash>`

Looks a hash up in paid rainbow-table services. Results are cached and shared
between all keys: a hit is cached permanently, a miss for 24h by default. One
hash per call.

```bash
relay lookup-hash e10adc3949ba59abbe56e057f20f883e
relay lookup-hash 8846f7eaee8fb117ad06bdd830b7586c --type ntlm
```

| Field | Default | Notes |
| --- | --- | --- |
| hash (the word) | required | Lower-cased and trimmed by the relay. Empty is `400 EMPTY_HASH` |
| `--type` | auto-detect | `md5`, `sha1`, `sha256`, `sha384`, `sha512`, `ntlm`, `mysql` or `mysql5`. Set it when the length is ambiguous (32 hex chars: md5 vs ntlm); omit it if unsure |
| `--group` | `hash_lookup` | Routing group |
| `--cache=false` | on | Keep the cache on: off goes upstream and can be charged again |

- `found: false` is a normal answer, not an error (`plaintext` is then
  omitted). Do not retry it in a loop.
- `cache_hit: true` means no upstream was called and nothing was counted
  against the daily quota.
- A request with `--type` is answered from cache only by an entry stored with
  the same type; without it, by any entry for that hash.
- `400 INVALID_HASH_TYPE` / `INVALID_HASH`: `--type` or the hash is invalid.
  `400 UNSUPPORTED_BY_GROUP`: no backend in the routing group reverses that
  type; drop `--type` to auto-detect, or stop.
- `504 UPSTREAM_TIMEOUT` / `REQUEST_TIMEOUT`: not sent to another backend and
  may have been charged. If the charge matters, find out before retrying;
  retry at most once.
- Lookups cost money per hash. Deduplicate hashes first, and skip salted or
  bcrypt/argon2 hashes — rainbow tables cannot answer them.
- Recovered plaintexts are credentials: report them only as the task requires.
