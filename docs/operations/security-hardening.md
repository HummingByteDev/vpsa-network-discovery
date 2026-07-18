# Security Hardening & Compromise Response

Threat model recap (details: [architecture/05-security-trust-model.md](../architecture/05-security-trust-model.md)):
malicious workers, stolen worker credentials, and unreliable measurements
are *assumed* and handled in-protocol (signatures, replay protection,
consensus, trust weighting, quarantine). This document covers the layer the
protocol cannot handle: the platform host and the platform secrets.

## Stack hardening (shipped by default in deploy/prod)

- Distroless, CGO-free images; `read_only: true` root filesystems for
  coordinator/aggregator; no shell in production containers.
- Single exposed service (Caddy 80/443). `/metrics`, postgres, aggregator:
  internal network only. `/admin/v1` CIDR-allowlisted at the edge **and**
  bearer-token authenticated at the coordinator.
- Secrets only in `.env` (chmod 600) — never in images or the repo.
- Log rotation bounded (json-file 20 MB × 5 per service).
- The dev-only `VAPN_DEV_ENROLLMENT_TOKEN` is absent from the production
  compose — enrollment always requires per-worker one-time tokens.

## Host checklist (Ubuntu LTS)

- `ufw default deny incoming; ufw allow 22,80,443/tcp; ufw enable`
  (Docker publishes only through Caddy, but belt-and-suspenders).
- SSH: keys only, no root login; `unattended-upgrades` for the OS.
- Time sync on (`timedatectl`) — wireauth tolerates ±2 min skew; a drifting
  platform clock would lock the whole fleet out.
- Disk headroom alerting (the builder needs ~10 GB free per run).
- The VM is single-purpose: nothing else runs on it.

## Credential inventory & rotation

| Credential | Held by | Rotate |
|---|---|---|
| `VAPN_ADMIN_TOKEN` | operators, vapnctl | edit `.env`, `up -d` coordinator; update operator shells |
| `VAPN_ADVISOR_TOKEN` | platform ↔ website | issue second token website-side, swap, revoke old |
| S3 key pair | builder, coordinator | issue new key at provider, swap, revoke |
| DB password | in-stack services | edit `.env`, `up -d` (postgres + services) |
| Snapshot signing key | builder only | see below — expensive, plan it |
| Worker keys | each worker | self-rotating; admin can demand via `vapnctl workers rotate-key` |

Signing-key rotation: generate new pair → ship new public key in a worker
release (workers accept it after update) → switch the builder's private key
→ next snapshot signed with the new key. Old-key workers keep their last
snapshot until they update; plan a deprecation window.

## Compromise response

### A worker is compromised or malicious

Protocol already contains it (trust weighting + consensus). Operationally:
`vapnctl workers suspend <id> --reason ...` (locks out at the next signed
request), or `quarantine` to keep observing behavior with zero public
weight. Evidence: `vapnctl workers show <id>`, `vapnctl audit`,
`registry.trust_event`. Aggregation is *not* poisoned retroactively — if a
long-trusted worker turns out bad, its windows can be recomputed
(measurements are per-worker attributed and signed).

### The admin token leaks

Rotate immediately (table above). Review `vapnctl audit --category admin
--since <suspected time>` — every admin action is audit-logged. Worst-case
admin powers: worker state changes, scheduler pause, snapshot rollback —
disruptive, recoverable; the token cannot exfiltrate keys or forge
measurements.

### The advisor credential leaks

It can read the monitoring catalog/enrollments and push fake statuses to
the website. Rotate website-side, then platform `.env`. Audit the website's
`monitoring_provider_status` for suspect writes.

### The platform host is compromised

Treat everything on it as burned: rebuild from repo + secret store on a
fresh VM (backup-restore.md DR procedure), rotate **all** credentials above
including the signing key, and audit `registry`/`audit` schemas from the
last clean backup for planted workers or state changes. Workers are
unaffected infrastructure-wise (their keys never touch the platform) but
treat published snapshots and verdicts from the compromise window as
untrusted — roll back / recompute.

### The signing key leaks (without host compromise)

An attacker with the key + write access to the artifact store could feed
workers a malicious target list (probing victims). Workers cap damage:
ICMP-only probes, rate-limited, targets must be inside the snapshot.
Response: rotate signing key (procedure above), audit store access logs,
rotate S3 credentials.
