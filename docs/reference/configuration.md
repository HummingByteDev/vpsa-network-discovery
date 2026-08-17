# Configuration & Environment Variables

**This page is authoritative** for every setting VAPN reads: its name, type,
default, and meaning. Other documents explain configuration in context and link
here rather than repeating it.

Every VAPN service and CLI is configured through **`VAPN_`-prefixed environment
variables** (12-factor). There are no config files to hand-edit for the
services; the worker keeps its settings in `~/.vapn/config.env`, written by
`vapn install`.

- **Setting up a builder/platform for the first time?** → [Install the builder](../builder/installation.md)
  walks through only the settings you actually need.
- **Setting up a worker?** → [Install a worker](../worker/installation.md).

## The configuration model

Config is loaded through `internal/platform/config`, which gives every service
three guarantees:

1. **Typed access with defaults** — `String`, `Int`, `Bool`, `Duration`, and
   `Require` (mandatory). Bad values (a non-integer where an int is expected)
   are collected and reported together at boot.
2. **Fail-fast on missing required keys** — a service refuses to start and lists
   *every* missing or invalid variable at once, rather than crashing on the
   first:
   ```
   ERROR bad configuration error="configuration errors: VAPN_DB_DSN, VAPN_REDUNDANCY (not an integer: \"three\")"
   ```
3. **Redacted effective-config dump** — at startup each service logs its full
   effective configuration with secret-like keys shown as `[redacted]`. A key is
   treated as secret if its name contains `TOKEN`, `SECRET`, `KEY`, `PASSWORD`,
   `DSN`, or `CREDENTIAL`. This is the fastest way to confirm what a container
   actually received:
   ```
   INFO effective configuration VAPN_DB_DSN=[redacted] VAPN_HTTP_ADDR=:8080 …
   ```

Durations use Go syntax (`30s`, `2m`, `6h`). Booleans accept `true`/`false`
(and the other forms Go's `strconv.ParseBool` allows).

---

## Shared

Read by more than one service.

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_DB_DSN` | string **(required)** | — | PostgreSQL connection string. Use a per-service, least-privilege role |
| `VAPN_DB_WAIT` | duration | `60s` | How long to wait for the database to become reachable at boot |
| `VAPN_HTTP_ADDR` | string | varies | Listen address — `:8080` coordinator, `:8082` aggregator, `:8081` mockadvisor |
| `VAPN_LOG_LEVEL` | string | `info` | `debug` \| `info` \| `warn` \| `error`. Anything else is treated as `info` |
| `VAPN_ADVISOR_URL` | string | see per-service | VPS Advisor **base** URL. Must have no path and must not redirect — see below |
| `VAPN_ADVISOR_TOKEN` | string (secret) | see per-service | Service credential for the VPS Advisor API |

**`VAPN_ADVISOR_URL` must be the exact address the site answers on.** Two
mistakes account for nearly every "the platform can't see the website" report,
and both are checked at startup — the affected service logs the error and the
URL it would call:

- **A path.** `https://site.example/api` becomes
  `https://site.example/api/api/v1/monitoring/...` and 404s, because the client
  appends `/api/v1/monitoring/...` itself. Give the bare address.
- **A redirect.** If `https://www.site.example` 301s to `https://site.example`
  (or the reverse), the platform will **not** follow it: a redirect across
  hosts strips the `Authorization` header, so following one turns every
  authenticated pull into an anonymous 401 that looks like a bad credential.
  Configure the address the site serves directly.

Everything the platform learns from VPS Advisor flows through this one value —
the provider catalog every build starts from, worker enrolments, and
administrator approvals. A wrong value therefore shows up as three seemingly
unrelated faults at once: no snapshots published, workers that never appear,
and approved workers stuck `pending`. Check it first with `vapnctl status`,
which reports each feed's health.

### Artifact store (`VAPN_ARTIFACT_*`)

Provider-agnostic S3-compatible storage for snapshot artifacts. Switching
providers is an environment change only, never code. Written by the **builder**,
read by the **coordinator**.

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_ARTIFACT_S3_ENDPOINT` | string | — | Endpoint **host** (`host[:port]`, no scheme) |
| `VAPN_ARTIFACT_S3_REGION` | string | — | Region. AWS requires it; R2 uses `auto`; B2 and MinIO infer it |
| `VAPN_ARTIFACT_S3_BUCKET` | string | `vapn-artifacts` | Bucket name |
| `VAPN_ARTIFACT_S3_ACCESS_KEY` | string (secret) | — | Access key / key ID / application key ID |
| `VAPN_ARTIFACT_S3_SECRET_KEY` | string (secret) | — | Secret key / application key |
| `VAPN_ARTIFACT_S3_USE_SSL` | bool | `true` | Set `false` only for local MinIO |
| `VAPN_ARTIFACT_DIR` | string | — | Filesystem store **instead of** S3 (tests, single host) |

`VAPN_ARTIFACT_S3_ENDPOINT` and `VAPN_ARTIFACT_DIR` are **mutually exclusive** —
setting both is a startup error. Setting neither means no store: the
**coordinator refuses to start**, and the **builder runs in build-only mode**,
logging a warning that the snapshot will not be distributable to workers.

The endpoint is a **host**: no scheme, no bucket name, no path. A pasted
`https://` prefix is stripped for you, but anything after the host is not.

| Provider | `…_S3_ENDPOINT` | `…_S3_REGION` | Credentials |
|---|---|---|---|
| Backblaze B2 | `s3.<region>.backblazeb2.com` | `<region>`, e.g. `us-east-005` (optional; inferred if empty) | **Application** key ID + key. The account **master key does not work with the S3 API** |
| Cloudflare R2 | `<account-id>.r2.cloudflarestorage.com` | `auto` (literal) | R2 API token → Access Key ID + Secret Access Key |
| AWS S3 | `s3.<region>.amazonaws.com` | `<region>` — **required** | IAM access key + secret |
| MinIO (dev) | `localhost:9000` | — | root user + password; also set `VAPN_ARTIFACT_S3_USE_SSL=false` |

A ready-to-paste block per provider is in
[`deploy/prod/.env.example`](../../deploy/prod/.env.example).

**The bucket must already exist** — the builder uploads into it and never
creates it, so a key scoped to one bucket is enough (and is what you want).
Objects appear only when a build runs: the builder is a scheduled one-shot, so
an empty bucket after `docker compose up -d` is expected until
`vapn-builder.timer` fires or you run a build by hand. See
[builder installation](../builder/installation.md).

---

## Builder

Run as a one-shot job. See [Install the builder](../builder/installation.md).

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_DB_DSN` | string **(required)** | — | Database (builder role) |
| `VAPN_DB_WAIT` | duration | `60s` | Boot wait for the database |
| `VAPN_ADVISOR_URL` | string | `http://localhost:8081` | Monitored-ASN source |
| `VAPN_ADVISOR_TOKEN` | string **(required, secret)** | — | VPS Advisor credential |
| `VAPN_RIS_BVIEW_URL` | string | — | RIS `bview` download URL. **Empty means no download** — the file must be supplied out of band at `VAPN_RIS_BVIEW_PATH` (how the dev stack works) |
| `VAPN_RIS_BVIEW_PATH` | string | `data/ripe/latest-bview.gz` | Local path the dump is cached at and read from |
| `VAPN_RIS_BVIEW_MAX_AGE` | duration | `6h` | Re-download when the cached copy is older than this. A fresher copy is reused as-is |
| `VAPN_GEOIP_CITY_MMDB` | string | — | Path to GeoLite2-City. **Empty skips enrichment** with a warning; set-but-missing is a build failure |
| `VAPN_MAX_TARGETS_PER_PROVIDER` | int | `100` | Probe-target cap, **per provider per address family** |
| `VAPN_SANITY_MAX_DELTA_PCT` | int | `50` | Hold the snapshot if the total prefix count swings past this percentage versus the published one |
| `VAPN_SANITY_FORCE` | bool | `false` | Publish even when the sanity gate trips. Use per-run, never in `.env` |
| `VAPN_RETAIN_SNAPSHOTS` | int | `5` | Superseded snapshots kept un-pruned (and therefore rollback-eligible) |
| `VAPN_SNAPSHOT_SIGNING_KEY` | string **(secret)** | — | Base64 Ed25519 **seed** (32 bytes) used to sign manifests. Required whenever an artifact store is configured |
| `VAPN_MIN_WORKER_VERSION` | string | `0.1.0` | Written into each manifest; workers below it refuse the snapshot |
| `VAPN_ARTIFACT_*` | — | — | [Artifact store](#artifact-store-vapn_artifact_) — where the snapshot is published |

**Exit codes:** `0` published · `2` held by the sanity gate · `1` error.

---

## Coordinator

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_HTTP_ADDR` | string | `:8080` | Worker-facing listen address |
| `VAPN_DB_DSN` | string **(required)** | — | Database |
| `VAPN_DB_WAIT` | duration | `60s` | Boot wait for the database |
| `VAPN_ADMIN_TOKEN` | string **(required, secret)** | — | Bearer token for the [admin API](../api/README.md#c-platform-admin-api) and `vapnctl` |
| `VAPN_DEV_ENROLLMENT_TOKEN` | string (secret) | — | **Dev only.** A shared token that auto-enrolls and auto-approves any worker presenting it. The coordinator logs a warning when set; never use it in production |
| `VAPN_SCHEDULER_INTERVAL` | duration | `30s` | How often the scheduler rebalances |
| `VAPN_REDUNDANCY` | int | `3` | Distinct workers per target |
| `VAPN_MAX_ASSIGNMENTS_PER_WORKER` | int | `64` | Cap on concurrent assignments per worker |
| `VAPN_GEOIP_ASN_MMDB` | string | — | Path to GeoLite2-ASN, for resolving each worker's source ASN (diversity + self-ASN exclusion) |
| `VAPN_ADVISOR_URL` | string | — | VPS Advisor base URL (catalog, enrollments, decisions) |
| `VAPN_ADVISOR_TOKEN` | string (secret) | — | VPS Advisor credential |
| `VAPN_ADVISOR_SYNC_INTERVAL` | duration | `2m` | Provider / decision / enrollment poll cadence |
| `VAPN_ARTIFACT_*` | — | — | [Artifact store](#artifact-store-vapn_artifact_) — **mandatory**; the coordinator exits if none is configured |

> `VAPN_ADMIN_ALLOW_CIDR` is **not** read by the coordinator. It is consumed by
> **Caddy** (`deploy/prod/Caddyfile`) to allowlist `/admin/v1/*` at the edge —
> see [Edge & deployment](#edge--deployment-consumed-by-composecaddy-not-the-services).
> The coordinator's own defence is `VAPN_ADMIN_TOKEN`.

---

## Aggregator

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_HTTP_ADDR` | string | `:8082` | Health/metrics listen address |
| `VAPN_DB_DSN` | string **(required)** | — | Database |
| `VAPN_DB_WAIT` | duration | `60s` | Boot wait for the database |
| `VAPN_TRUST_INTERVAL` | duration | `1m` | Trust recomputation cadence |
| `VAPN_WINDOW_SECONDS` | int | `300` | Consensus window length, in seconds |
| `VAPN_MIN_WORKERS` | int | `3` | Distinct workers required for a verdict (otherwise `insufficient_data`) |
| `VAPN_ADVISOR_URL` | string | — | Results push target. **Empty disables publication** — the outbox accumulates and the aggregator logs a warning |
| `VAPN_ADVISOR_TOKEN` | string (secret) | — | VPS Advisor credential |

Consensus tuning beyond these (healthy/degraded ratios, latency factor,
retention windows) and the outbox drain cadence are code constants — see
[`internal/aggregate`](../../internal/aggregate/aggregate.go) and
[trust calculation](../walkthroughs/trust-calculation.md).

---

## Worker

The container reads these; `vapn install` writes them to `~/.vapn/config.env`.

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_COORDINATOR_URL` | string **(required)** | — | Coordinator endpoint the worker talks to |
| `VAPN_SNAPSHOT_PUBLIC_KEY` | string **(required)** | — | Base64 Ed25519 public key (32 bytes) the worker verifies every snapshot against. Not secret — but a wrong value means every snapshot is refused |
| `VAPN_ENROLLMENT_TOKEN` | string (secret) | — | One-time enrollment token. Needed only on first boot; the CLI deletes it from `config.env` after successful registration |
| `VAPN_WORKER_NAME` | string | — | Human-friendly name reported at registration |
| `VAPN_STATE_DIR` | string | `/state` | Where identity, snapshot, and status live **inside the container** |
| `VAPN_HEARTBEAT_INTERVAL` | duration | `30s` | Heartbeat cadence |

### `vapn` CLI (host side)

| Variable | Default | Meaning |
|---|---|---|
| `VAPN_HOME` | `~/.vapn` | Worker home directory: `config.env`, `docker-compose.yml`, `state/` |
| `VAPN_WORKER_IMAGE` | `ghcr.io/hummingbytedev/vapn-worker:latest` | Image to run. Set it in the environment to pin a version at install time |

### `install.sh`

| Variable | Default | Meaning |
|---|---|---|
| `VAPN_DOWNLOAD_BASE` | latest GitHub release download URL | Where to fetch the `vapn` binary from |
| `VAPN_BIN_DIR` | `/usr/local/bin` (falls back to `~/.local/bin`) | Install target for the CLI |

---

## `vapnctl` (platform administration)

Flags take precedence; these are the fallbacks.

| Variable | Meaning |
|---|---|
| `VAPN_COORDINATOR_URL` | Coordinator base URL (`--url`) |
| `VAPN_ADMIN_TOKEN` | Admin bearer token (`--token`) |

---

## Migrate

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_DB_DSN` | string **(required)** | — | Database to migrate |
| `VAPN_DB_WAIT` | duration | `60s` | Boot wait for the database |
| `VAPN_MIGRATIONS_DIR` | string | `migrations` | Directory of ordered SQL files. The container image sets `/migrations` |

## Mock advisor (dev/CI only)

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `VAPN_HTTP_ADDR` | string | `:8081` | Listen address |
| `VAPN_ADVISOR_TOKEN` | string | `dev-advisor-token` | Token the stub requires |
| `VAPN_MOCK_FIXTURES` | string | — | Path to a fixtures JSON file. Empty uses the built-in fixtures |

## Test / development

| Variable | Default | Meaning |
|---|---|---|
| `VAPN_TEST_DB_DSN` | `postgres://vapn:vapn-dev@localhost:5433/vapn_test` | Test database DSN. Tests **never** touch the live dev database — they truncate and reshape what they touch |

---

## Snapshot signing keys

Generate the pair once, with either:

```sh
docker build --build-arg COMPONENT=keygen -t vapn-keygen .   # from the repo root
docker run --rm vapn-keygen
```

```sh
make build && ./bin/keygen                                    # needs Go
```

Both print exactly two lines:

```
VAPN_SNAPSHOT_SIGNING_KEY=<base64 32-byte Ed25519 seed>    ← builder only, SECRET
VAPN_SNAPSHOT_PUBLIC_KEY=<base64 32-byte Ed25519 public>   ← every worker, public
```

The **builder** holds the private half and signs each manifest; every **worker**
pins the public half and refuses any snapshot that doesn't verify against it.
Rotation means generating a new pair, distributing the new public key to worker
operators, then switching the builder's private key — see
[Security → credential inventory](../operations/security.md#credential-inventory--rotation)
and the [installation guide's key step](../builder/installation.md#step-3--generate-the-snapshot-signing-key).

---

## Edge & deployment (consumed by Compose/Caddy, not the services)

These appear in `deploy/prod/.env` and are read by the compose file or the
Caddyfile at startup — not by any Go service. (The backup script's variables
look similar but are **not** read from `.env`; see
[below](#backup-script-variables--not-read-from-env).)

| Variable | Default | Meaning |
|---|---|---|
| `VAPN_DOMAIN` | — | Public domain; Caddy obtains TLS for it automatically |
| `VAPN_ADMIN_ALLOW_CIDR` | `127.0.0.1/32` | Space-separated CIDRs allowed to reach `/admin/v1/*` through the edge |
| `VAPN_VERSION` | `latest` | Image tag the stack runs |
| `VAPN_IMAGE_PREFIX` | `ghcr.io/hummingbytedev/vapn` | Image name prefix (each component appends `-<component>`) |
| `VAPN_DB_PASSWORD` | — | Password for the in-stack postgres; also composed into every service's `VAPN_DB_DSN` |
| `VAPN_GEOIP_DIR` | `./geoip` | Host directory holding the GeoLite2 databases, mounted into builder and coordinator |
| `MAXMIND_ACCOUNT_ID` | — | MaxMind account (note: **no** `VAPN_` prefix) |
| `MAXMIND_LICENSE_KEY` | — | MaxMind licence key (secret; no `VAPN_` prefix) |
| `VAPN_GRAFANA_PASSWORD` | `admin` | Grafana admin password (monitoring profile) |

### Backup script variables — **not** read from `.env`

`scripts/backup.sh` reads these from its **process environment**, and the
shipped `vapn-backup.service` has no `EnvironmentFile=`. Putting them in
`deploy/prod/.env` therefore has **no effect on the scheduled backup**. Set them
on the unit instead:

```sh
sudo systemctl edit vapn-backup.service
```
```ini
[Service]
Environment=VAPN_BACKUP_S3_URI=s3://your-bucket/vapn-backups
Environment=VAPN_BACKUP_KEEP=14
```

| Variable | Default | Meaning |
|---|---|---|
| `VAPN_BACKUP_KEEP` | `14` | Dumps `scripts/backup.sh` retains locally |
| `VAPN_BACKUP_S3_URI` | — | Offsite destination, e.g. `s3://bucket/vapn-backups`. Also needs an `aws` or `mc` CLI configured for the user the timer runs as (root) |

They *do* work as expected when you invoke the script by hand:
`VAPN_BACKUP_S3_URI=s3://… ./scripts/backup.sh`.

## How it's set in each environment

- **Dev:** [`deploy/compose/dev.compose.yaml`](../../deploy/compose/dev.compose.yaml)
  sets almost everything inline — postgres, MinIO, and the mockadvisor endpoints
  are literals — and pre-downloaded `data/` is mounted so no external fetch is
  needed.

  > ⚠️ **The two snapshot keys are the exception, and they fail quietly.** They
  > are the only builder/worker settings taken from interpolation:
  >
  > ```yaml
  > VAPN_SNAPSHOT_SIGNING_KEY: ${VAPN_SNAPSHOT_SIGNING_KEY:-}   # builder
  > VAPN_SNAPSHOT_PUBLIC_KEY:  ${VAPN_SNAPSHOT_PUBLIC_KEY:-}    # workers
  > ```
  >
  > The `:-` default substitutes an **empty string** rather than failing, so a
  > build starts, connects to MinIO, and only then dies with
  > `signing key seed must be 32 bytes, got 0`. Workers fail earlier, with
  > `bad configuration: VAPN_SNAPSHOT_PUBLIC_KEY`.
  >
  > Compose reads interpolated values from your **shell** or from a `.env` in
  > the *project directory* (next to the compose file, or your working
  > directory) — **not** from an env file elsewhere in the repo. So either
  > export them:
  >
  > ```sh
  > eval "$(./bin/keygen | sed 's/^/export /')"
  > docker compose -f deploy/compose/dev.compose.yaml --profile build run --rm builder
  > ```
  >
  > …or keep them in a file and pass it on **every** compose command, `up`
  > included:
  >
  > ```sh
  > docker compose --env-file path/to/.env \
  >   -f deploy/compose/dev.compose.yaml --profile build run --rm builder
  > ```
  >
  > Confirm before you run anything: `docker compose … config | grep SNAPSHOT`
  > shows the resolved values.
- **Production:** [`deploy/prod/.env.example`](../../deploy/prod/.env.example)
  plus `docker-compose.yml`; secrets live in `.env` (chmod 600) outside version
  control, or in a secrets manager. Walkthrough:
  [Install the builder](../builder/installation.md).
