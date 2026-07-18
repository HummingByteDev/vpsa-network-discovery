# Production Deployment

The officially supported v1 topology: **one Ubuntu LTS VM, Docker Compose,
Caddy at the edge**, PostgreSQL in the stack, artifacts in any S3-compatible
store. It comfortably serves several hundred community workers. Every service
is stateless apart from postgres and the object store, so a later migration
to Kubernetes is a packaging change, not an architecture change.

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
              S3-compatible object store (B2 / R2 / AWS / minio)
```

Only Caddy has published ports. `/api/v1/*` is public (workers), `/admin/v1/*`
is CIDR-allowlisted, everything else is internal.

## Prerequisites

- Ubuntu LTS VM: 4 vCPU / 8 GB RAM / 80 GB disk is comfortable. The builder
  parses a ~4 GB MRT dump per run — disk and memory headroom matter more
  than steady-state load.
- A DNS A/AAAA record for your platform domain pointing at the VM.
- Docker Engine + Compose plugin (`curl -fsSL https://get.docker.com | sh`).
- An S3-compatible bucket + key pair (Backblaze B2 recommended; Cloudflare
  R2 and AWS S3 are drop-in — endpoint/region/credentials in `.env` only).
- A MaxMind account for GeoLite2 (free; **bring your own license key** — the
  databases must not be redistributed).
- The VPS Advisor service credential (or run the mockadvisor stub while the
  website integration is pending).

## Install

```sh
sudo mkdir -p /opt/vapn && sudo chown "$USER" /opt/vapn
git clone https://github.com/HummingByteDev/vpsa-network-discovery /opt/vapn
cd /opt/vapn/deploy/prod
cp .env.example .env
```

Fill in `.env` (every value; see comments inline). Generate secrets:

```sh
openssl rand -hex 32          # VAPN_DB_PASSWORD, VAPN_ADMIN_TOKEN
docker compose run --rm --entrypoint /keygen builder   # snapshot signing keypair
```

Keep `VAPN_SNAPSHOT_SIGNING_KEY` in `.env` (chmod 600) and record the public
key — workers pin it. **Losing the private key means re-issuing the public
key to every worker; treat it like a CA key.**

Bring the stack up:

```sh
docker compose up -d                         # edge, db, coordinator, aggregator
docker compose --profile geoip up -d         # MaxMind updater (BYO key)
docker compose --profile monitoring up -d    # prometheus + grafana (recommended)
```

Install the scheduled jobs:

```sh
sudo cp systemd/vapn-*.{service,timer} /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now vapn-builder.timer vapn-backup.timer
sudo systemctl start vapn-builder.service    # first snapshot build right now
```

The first build downloads the RIS bview (~4 GB), extracts monitored
prefixes, publishes the signed artifact, and exits 0. Verify:

```sh
export VAPN_COORDINATOR_URL=https://$YOUR_DOMAIN VAPN_ADMIN_TOKEN=...
vapnctl status          # snapshot version + target count should be populated
vapnctl snapshots list
```

## First workers

Until the VPS Advisor enrollment UI ships, enroll via the platform:

```sh
vapnctl workers create --name anchor-fra-1   # prints one-time token
# operator runs the vapn installer with that token (docs/worker/install.md)
vapnctl workers approve <worker-id> --reason "anchor node"
```

## Notes

- **Health checks**: every service self-reports; `docker compose ps` shows
  health. Externally, `https://domain/api/v1/...` returning 401 (unsigned) is
  the cheap liveness signal — the edge and coordinator are up.
- **Scaling within the VM**: the coordinator is stateless —
  `docker compose up -d --scale coordinator=2` works behind Caddy if a single
  process ever saturates (load tests put that point well past 500 workers).
- **External postgres**: point `VAPN_DB_DSN` env overrides at a managed
  instance and drop the postgres service; nothing else changes.
- **Kubernetes**: not supported as the v1 target, deliberately. All state
  lives in postgres + object store, config is env-only, images are distroless
  — when the fleet outgrows one VM, the same images move into any orchestrator.
