---
type: Tool Playbook
title: zombie
description: Use this playbook when working with zombie for authorized weak credential checks and authentication result analysis.
tags: [easm, weakpass]
status: stable
generated:
  actor: process:okf-migration
  at: 2026-08-02
---

# Zombie

Zombie is the weak credential checking tool in aiscan.

Capabilities:

- test supported network services for weak credentials when authorized
- report service URI, protocol, host, port, module, username, password, and authentication result
- distinguish successful credentials, failed attempts, connection errors, lockouts, and unsupported services
- expose retry, timeout, and module-specific messages

Common usage:

```bash
zombie -i 192.168.1.1:3306 -s mysql --concurrency 8
zombie -i 192.168.1.1:22 -s ssh -u root -p admin123,123456,root --concurrency 8
zombie -i 192.168.1.1:6379 -s redis --concurrency 8
zombie -i 192.168.1.1:15672 -s rabbitmq --concurrency 8
zombie -I targets.txt -s ssh --top 10 --concurrency 8
zombie -i 192.168.1.1:8080 -s tomcat -u admin,tomcat -p admin,tomcat,s3cret --concurrency 8
zombie -i 192.168.1.1:23 -s telnet -u admin -p admin --concurrency 8
```

Key flags:

- `-i`: target ip:port (can repeat: `-i host1:3306 -i host2:3306`). Also accepts `ssh://user@host:22` URL format.
- `-s`: service name — **required** when target has no scheme prefix. Services: ssh, mysql, redis, ftp, rdp, smb, tomcat, nacos, minio, rabbitmq, etc.
- `-u`: username(s), comma-separated or repeated.
- `-p`, `--pwd`: password(s), comma-separated or repeated. Prefer `-p`. Do not use `--password`; upstream zombie does not define it.
- `-I`: target file (uppercase I, one ip:port per line).
- `--top N`: use top N common passwords from built-in dictionary.
- `--concurrency N`: max concurrent connections per host. Use `--concurrency 8` by default, especially for Telnet/Comware-style devices.
- `-t`: global thread count (default 100). **Not** target — use `-i` for target. Do not use `-t` as the default per-host concurrency limiter when `--concurrency` is available.
- `-c`: CIDR input. **Not** concurrency; do not emit `-c 8`.
- `-l`: **list supported services and exit** (not a file flag!).

Common mistakes:

```bash
# WRONG:
zombie -t 10.0.0.1 -p 3306 -s mysql    # -t is threads, not target
zombie -l targets.txt -s ssh            # -l lists services, not reads file
zombie -i 10.0.0.1:3306                 # missing -s mysql
zombie -i 10.0.0.1:23 -s telnet --password admin  # use -p/--pwd for passwords
zombie -i 10.0.0.1:23 -s telnet -p admin -c 8     # -c is CIDR, not concurrency

# RIGHT:
zombie -i 10.0.0.1:3306 -s mysql --concurrency 8
zombie -I targets.txt -s ssh --concurrency 8
zombie -i 10.0.0.1:3306 -s mysql -t 50 --concurrency 8
zombie -i 10.0.0.1:23 -s telnet -p admin --concurrency 8
```
