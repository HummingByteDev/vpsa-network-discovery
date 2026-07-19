# Configuration & Environment Variables

Every VAPN service and CLI is configured through **`VAPN_`-prefixed environment
variables** (12-factor). There are no config files to hand-edit for the
services; the worker keeps its two settings in `~/.vapn/config.env`, written by
`vapn install`.

## The configuration model

Config is loaded through `internal/platform/config`, which gives every service
three guarantees:

1. **Typed access with defaults** — `String`, `Int`, `Bool`, `Duration`, and
   `Require` (mandatory). Bad values (a non-integer where an int is expected)
   are collected and reported together at boot.
2. **Fail-fast on missing required keys** — a service refuses to start and lists
   *every* missing/invalid variable at once, rather than crashing on the first.
3. **Redacted effective-config dump** — at startup each service logs its full
   effective configuration with secret-like keys (anything containing `TOKEN`,
   `SECRET`, `KEY`, `PASSWORD`, `DSN`, `CREDENTIAL`) shown as `[redacted]`. This
   is the fastest way to confirm what a container actually received.

```
INFO effective configuration VAPN_DB_DSN=[redacted] VAPN_HTTP_ADDR=:8080 …
```

## Shared variables

Used by multiple services:

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_DB_DSN` | string (req.) | — | PostgreSQL connection string (per-service least-privilege role) |
| `VAPN_DB_WAIT` | duration | `60s` | How long to wait for the DB to become reachable at boot |
| `VAPN_HTTP_ADDR` | string | varies | Listen address (`:8080` coordinator, `:8082` aggregator) |
| `VAPN_LOG_LEVEL` | string | `info` | slog level |
| `VAPN_ADVISOR_URL` | string | — | VPS Advisor base URL |
| `VAPN_ADVISOR_TOKEN` | string | — | Service credential for the VPS Advisor API |

### Artifact store (`VAPN_ARTIFACT_*`)

Provider-agnostic S3-compatible storage for snapshot artifacts. Switching
providers is an environment change only, never code. Used by the builder
(writes) and coordinator (serves).

| Variable | Meaning |
|---|---|
| `VAPN_ARTIFACT_S3_ENDPOINT` | S3 endpoint host |
| `VAPN_ARTIFACT_S3_REGION` | Region (`auto` for R2) |
| `VAPN_ARTIFACT_S3_BUCKET` | Bucket (default `vapn-artifacts`) |
| `VAPN_ARTIFACT_S3_ACCESS_KEY` / `_SECRET_KEY` | Credentials |
| `VAPN_ARTIFACT_S3_USE_SSL` | `true` in prod, `false` for local MinIO |
| `VAPN_ARTIFACT_DIR` | Filesystem store instead of S3 (tests/single host) |

Provider examples (endpoint mapping) are in
[architecture 07 §2](../architecture/07-deployment.md#production-platform-side).

## Coordinator

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_HTTP_ADDR` | string | `:8080` | Worker-facing listen address |
| `VAPN_DB_DSN` | string (req.) | — | Database |
| `VAPN_ADMIN_TOKEN` | string (req.) | — | Bearer token for the [admin API](../api/README.md#c-platform-admin-api) / `vapnctl` |
| `VAPN_ADMIN_ALLOW_CIDR` | string | `127.0.0.1/32` | CIDR allowlist for the admin surface (set via Caddy in prod) |
| `VAPN_DEV_ENROLLMENT_TOKEN` | string | — | Dev-only shortcut enrollment token |
| `VAPN_SCHEDULER_INTERVAL` | duration | `30s` | How often the scheduler rebalances |
| `VAPN_REDUNDANCY` | int | `3` | Distinct workers per target |
| `VAPN_MAX_ASSIGNMENTS_PER_WORKER` | int | `64` | Cap on concurrent assignments per worker |
| `VAPN_GEOIP_ASN_MMDB` | string | — | Path to GeoLite2-ASN (worker source-ASN lookup) |
| `VAPN_ADVISOR_URL` / `_TOKEN` | string | — | VPS Advisor pull (catalog) + push |
| `VAPN_ADVISOR_SYNC_INTERVAL` | duration | `2m` | Provider/decision/enrollment poll cadence |

## Aggregator

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_HTTP_ADDR` | string | `:8082` | Health/metrics listen address |
| `VAPN_DB_DSN` | string (req.) | — | Database |
| `VAPN_TRUST_INTERVAL` | duration | `1m` | Trust recomputation cadence |
| `VAPN_WINDOW_SECONDS` | int | `300` | Consensus window length |
| `VAPN_MIN_WORKERS` | int | `3` | Distinct workers required for a verdict (else `insufficient_data`) |
| `VAPN_ADVISOR_URL` / `_TOKEN` | string | — | Results push target |

Consensus tuning (`HealthyRatio` 0.9, `DegradedRatio` 0.5, `LatencyFactor` 2.0,
retention windows) has code defaults; see [`internal/aggregate`](../../internal/aggregate/aggregate.go)
and [trust calculation](../walkthroughs/trust-calculation.md).

## Builder

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_DB_DSN` | string (req.) | — | Database (builder role) |
| `VAPN_ADVISOR_URL` | string | `http://localhost:8081` | Monitored-ASN source |
| `VAPN_ADVISOR_TOKEN` | string (req.) | — | VPS Advisor credential |
| `VAPN_RIS_BVIEW_URL` | string | — | RIS `bview` download URL (prod: `data.ris.ripe.net/rrc00/latest-bview.gz`) |
| `VAPN_RIS_BVIEW_PATH` | string | `data/ripe/latest-bview.gz` | Local dump path/cache |
| `VAPN_RIS_BVIEW_MAX_AGE` | duration | `6h` | Reject dumps older than this |
| `VAPN_GEOIP_CITY_MMDB` | string | — | Path to GeoLite2-City |
| `VAPN_MAX_TARGETS_PER_PROVIDER` | int | `100` | Probe-target cap per provider |
| `VAPN_SANITY_MAX_DELTA_PCT` | int | `50` | Hold for approval if prefix count swings past this % |
| `VAPN_SANITY_FORCE` | bool | `false` | Bypass the sanity gate |
| `VAPN_RETAIN_SNAPSHOTS` | int | `5` | Old snapshots to keep |
| `VAPN_SNAPSHOT_SIGNING_KEY` | string (secret) | — | Base64 Ed25519 private key for signing artifacts |
| `VAPN_MIN_WORKER_VERSION` | string | `0.1.0` | Oldest worker version allowed to use the snapshot |

## Worker

Written to `~/.vapn/config.env` by `vapn install`. Only two are required.

| Variable | Required | Meaning |
|---|---|---|
| `VAPN_COORDINATOR_URL` | yes | Coordinator endpoint the worker talks to |
| `VAPN_ENROLLMENT_TOKEN` | yes (first boot) | One-time enrollment token; spent on registration |
| `VAPN_MAXMIND_LICENSE_KEY` | no | Operator's own key for a local GeoIP DB (degrades gracefully) |
| `VAPN_HOME` | no | Override the `~/.vapn` home directory |
| `VAPN_STATE_DIR` | no | Override the state directory |
| `VAPN_WORKER_IMAGE` | no | Pin a specific worker image |
| `VAPN_WORKER_NAME` | no | Human-friendly worker name |

## Snapshot signing keys (`SNAPSHOT_SIGNING_KEY` / `SNAPSHOT_PUBLIC_KEY`)

Generate with `./bin/keygen`. The **builder** holds the private key
(`VAPN_SNAPSHOT_SIGNING_KEY`); the **worker image** pins the public key
(`VAPN_SNAPSHOT_PUBLIC_KEY`) so it can verify every artifact before use. Keep the
private key offline/secret; rotate by generating a new pair and shipping a worker
release with the new pinned public key. See
[builder installation](../builder/installation.md#step-1--generate-a-snapshot-signing-key).

## Test / development

| Variable | Meaning |
|---|---|
| `VAPN_TEST_DB_DSN` | Test database DSN (default `postgres://vapn:vapn-dev@localhost:5433/vapn_test`) — tests **never** touch the live dev DB |

## How it's set in each environment

- **Dev:** [`deploy/compose/dev.compose.yaml`](../../deploy/compose/dev.compose.yaml)
  sets everything; pre-downloaded `data/` is mounted so no external fetch is
  needed.
- **Production:** [`deploy/prod/.env.example`](../../deploy/prod/.env.example) +
  `docker-compose.yml`; secrets live in an env file outside the repo (or a
  secrets manager). Walkthrough: [deployment guide](../operations/deployment.md).
