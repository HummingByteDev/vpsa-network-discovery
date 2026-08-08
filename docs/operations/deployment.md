# Production Deployment

The officially supported v1 topology: **one Ubuntu LTS VM, Docker Compose,
Caddy at the edge**, PostgreSQL in the stack, artifacts in any S3-compatible
store. It comfortably serves several hundred community workers. Every service
is stateless apart from postgres and the object store, so a later migration to
Kubernetes is a packaging change, not an architecture change.

> **Installing for the first time?** Follow
> **[Install the builder](../builder/installation.md)** — it is the step-by-step
> guide that brings up this entire stack, from a fresh VPS to a published signed
> snapshot. This page is the operator's reference for the topology it produces:
> what talks to what, which pieces are optional, and how to vary it.

## What talks to what

```
workers (community, anywhere)             VPS Advisor website
        │ HTTPS                                   │ HTTPS
        ▼                                         │
   Caddy (443) ──► coordinator (8080) ◄──sync────┘
                        │    ▲                    ▲
                        ▼    │                    │ results push
                   postgres  │            aggregator (8082)
                        ▲    │ artifacts          ▲
                        │    ▼                    │
                   builder (scheduled)      postgres
                        │
                        ▼
              S3-compatible object store (B2 / R2 / AWS / MinIO)
```

Only Caddy has published ports. `/api/v1/*` is public (workers), `/admin/v1/*`
is CIDR-allowlisted, `/grafana/*` is served only with the monitoring profile,
and everything else — `/metrics`, `/healthz`, postgres, the aggregator — is
internal-network only.

## Prerequisites

- **Ubuntu LTS VM**: 4 vCPU / 8 GB RAM / 80 GB disk is comfortable. The builder
  parses a multi-gigabyte MRT dump per run — disk and memory headroom matter
  more than steady-state load.
- **A DNS A/AAAA record** for your platform domain pointing at the VM.
- **Docker Engine + Compose plugin** (`curl -fsSL https://get.docker.com | sh`).
- **An S3-compatible bucket + key pair.** Backblaze B2, Cloudflare R2 and AWS S3
  are all drop-in — endpoint, region and credentials live in `.env` only.
- **A MaxMind account** for GeoLite2 (free; bring your own licence key — the
  databases must not be redistributed).
- **The VPS Advisor service credential**, or the `mockadvisor` stub while the
  website integration is pending.

## The compose profiles

`deploy/prod/docker-compose.yml` ships four groups of services:

| Command | Brings up |
|---|---|
| `docker compose up -d` | caddy, postgres, migrate (one-shot), coordinator, aggregator — the platform |
| `docker compose --profile geoip up -d` | `geoipupdate`, refreshing GeoLite2 into `./geoip` every 72 h |
| `docker compose --profile monitoring up -d` | Prometheus (30-day retention) + Grafana with the VAPN Fleet dashboard |
| `docker compose run --rm builder` | One snapshot build, then exit — normally driven by `vapn-builder.timer` |

The builder is in the `build` profile precisely so it never starts as a
long-running service.

## Scheduled jobs

Two systemd units are shipped in `deploy/prod/systemd/` and expect the
repository at `/opt/vapn`:

| Timer | Schedule | Runs |
|---|---|---|
| `vapn-builder.timer` | 00:30, 08:30, 16:30 UTC (±10 min jitter) | `docker compose run --rm builder` |
| `vapn-backup.timer` | 03:15 UTC nightly (±15 min jitter) | `scripts/backup.sh` |

Installation and verification: [builder installation, Step 8](../builder/installation.md#step-8--run-the-builder-automatically).

## First workers

Until the VPS Advisor enrollment UI ships, enrol operators directly through the
platform:

```sh
vapnctl workers create --name anchor-fra-1     # prints a one-time token
# give the operator that token AND your snapshot public key;
# they run the installer: docs/worker/installation.md
vapnctl workers approve <worker-id> --reason "anchor node"
```

> **Both values are needed.** The enrollment token proves the operator is real;
> the **snapshot public key** (printed by `keygen`, and by the builder at the
> start of every run) is what lets their worker verify the routing snapshot.
> Publish the public key wherever you explain how to join.

Enrol **3–5 anchor workers you control, geographically spread**, before opening
community enrollment — consensus needs a trustworthy baseline, and
`VAPN_MIN_WORKERS` (default 3) is unreachable without one. See the
[launch checklist](launch-checklist.md).

## Operational notes

- **Health checks.** Every service self-reports; `docker compose ps` shows
  health. Externally, `https://$VAPN_DOMAIN/api/v1/workers/me` returning `401`
  (unsigned request correctly refused) is the cheap liveness signal that the
  edge and coordinator are up.
- **Scaling within the VM.** The coordinator is stateless —
  `docker compose up -d --scale coordinator=2` works behind Caddy if a single
  process ever saturates. [Load tests](monitoring.md#load-testing) put that
  point well past 500 workers.
- **External postgres.** Point `VAPN_DB_DSN` at a managed instance and drop the
  postgres service; nothing else changes.
- **CDN in front of artifacts.** Artifact distribution is CDN-offloadable from
  day one; pair the bucket with a CDN as the fleet grows.
- **Kubernetes** is not supported as the v1 target, deliberately. All state
  lives in postgres and the object store, config is environment-only, and the
  images are distroless — when the fleet outgrows one VM, the same images move
  into any orchestrator.

## Where to go next

| Task | Guide |
|---|---|
| Install the stack step by step | [Builder installation](../builder/installation.md) |
| Every setting and default | [Configuration reference](../reference/configuration.md) |
| Alerts and dashboards | [Monitoring](monitoring.md) |
| When an alert fires | [Runbooks](runbooks.md) |
| Backups and disaster recovery | [Backup & restore](backup-restore.md) |
| Upgrading and rolling back | [Releases & upgrades](releases-and-upgrades.md) |
| Hardening and incident response | [Security](security.md) |
| Before going public | [Launch checklist](launch-checklist.md) |
| The design record | [Architecture 07 — Deployment](../architecture/07-deployment.md) |
