# Builder Installation (Simplified)

A step-by-step guide to running the routing builder, written for someone
comfortable following commands but **not** assumed to be a Linux or networking
expert. If a term is unfamiliar, the [builder overview](README.md) and
[Glossary](../reference/glossary.md) explain it.

> **Who needs this?** Only **platform operators** — the people running VAPN
> itself. Community worker operators never run the builder;
> [running a worker](../getting-started/quickstart.md) is a completely separate,
> simpler task.

In production the builder runs as part of the platform's Docker Compose stack on
a schedule — you usually don't invoke it by hand. This guide covers both the
managed path (recommended) and a manual run for understanding and debugging.

## What you'll need

| Requirement | Why | How to get it |
|---|---|---|
| The VAPN platform stack running | The builder writes to the platform's PostgreSQL and artifact store | [Deployment guide](../operations/deployment.md) |
| A **MaxMind license key** | To download GeoLite2 for geolocation | Free account at maxmind.com → license key |
| A **snapshot signing key** | To sign artifacts so workers can trust them | Generate with `keygen` (below) |
| Access to **RIPE RIS** (outbound HTTPS) | To download the routing dump | Just outbound internet; no account needed |
| The **VPS Advisor service token** | To fetch the monitored ASN list | From the website team |

## Step 1 — Generate a snapshot signing key

Workers verify every snapshot against a **pinned public key**. You generate an
Ed25519 keypair once; the builder holds the private half, and the public half is
baked into the worker image.

```sh
./bin/keygen
# prints a base64 private key and its public key
```

Keep the **private key** secret (a password manager or secrets store — ideally
injected per-run and kept offline-capable). Publish the **public key** to the
worker image / pinned config. Losing the private key just means generating a new
one and rotating; leaking it would let someone forge snapshots, so treat it like
a signing certificate.

## Step 2 — Configure the builder

The builder is configured entirely through `VAPN_`-prefixed environment
variables (it prints its effective configuration at startup, with secrets
redacted). The important ones:

| Variable | Purpose | Example / default |
|---|---|---|
| `VAPN_DB_DSN` | PostgreSQL connection (builder role) | `postgres://vapn:…@postgres:5432/vapn` |
| `VAPN_ADVISOR_URL` | VPS Advisor base URL | `https://vpsadvisor.example` |
| `VAPN_ADVISOR_TOKEN` | Service credential for the ASN list | *(secret)* |
| `VAPN_RIS_BVIEW_URL` | Where to download the RIS dump | `https://data.ris.ripe.net/rrc00/latest-bview.gz` |
| `VAPN_RIS_BVIEW_PATH` | Local path/cache for the dump | `/work/latest-bview.gz` (dev: `data/ripe/latest-bview.gz`) |
| `VAPN_RIS_BVIEW_MAX_AGE` | Reject dumps older than this | `6h` |
| `VAPN_GEOIP_CITY_MMDB` | Path to GeoLite2-City | `/geoip/GeoLite2-City.mmdb` |
| `VAPN_SNAPSHOT_SIGNING_KEY` | Base64 private signing key (Step 1) | *(secret)* |
| `VAPN_MIN_WORKER_VERSION` | Oldest worker version allowed to use the snapshot | `0.1.0` |
| `VAPN_MAX_TARGETS_PER_PROVIDER` | Cap on probe targets per provider | `100` |
| `VAPN_SANITY_MAX_DELTA_PCT` | Hold for approval if prefix count swings more than this % | `50` |
| `VAPN_SANITY_FORCE` | Bypass the sanity gate (use sparingly) | `false` |
| `VAPN_RETAIN_SNAPSHOTS` | How many old snapshots to keep | `5` |
| `VAPN_ARTIFACT_S3_*` | Artifact store (endpoint, bucket, keys) | see [deployment](../operations/deployment.md) |

The full list with defaults is in the
[configuration reference](../reference/configuration.md).

## Step 3 — Run it

### Managed (recommended): the production stack

In the production Compose stack the builder is wired as a scheduled job via
systemd timer units shipped in the repo:

```
deploy/prod/systemd/vapn-builder.service
deploy/prod/systemd/vapn-builder.timer
```

Install and enable the timer (runs the builder daily):

```sh
sudo cp deploy/prod/systemd/vapn-builder.* /etc/systemd/system/
sudo systemctl enable --now vapn-builder.timer
systemctl list-timers vapn-builder.timer      # confirm next run
```

The Compose file already mounts the GeoIP volume (kept fresh by the
`geoipupdate` container) and passes the environment. See the
[deployment guide](../operations/deployment.md) for the whole stack.

### Manual: one run, for understanding or debugging

```sh
# from a checkout, with the env vars above exported:
make build
./bin/builder
```

It will log each stage — sync, download, parse, filter, validate, enrich, load,
target-derivation, sanity gate, export, publish — and exit. In development the
defaults point at the pre-downloaded `data/ripe/` and `data/geo-data/`, so it
runs fully offline.

## Step 4 — Verify it worked

```sh
# Are snapshots being published?
./bin/vapnctl snapshots list
# → shows versions, status (published/superseded/failed), counts, timestamps
```

A healthy result: a recent snapshot with status **`published`**, a sensible
prefix count, and a `built_at` within your cadence. You can also check that
workers see it — `vapn status` on a worker shows the snapshot version it holds.

## Updating the builder

The builder is just a container image in the platform stack. Update it with the
rest of the platform during a normal [platform upgrade](../operations/upgrades.md)
(pull the new image tag, let the timer run the new version). There's no separate
builder update ceremony — because it's a batch job, the next scheduled run
simply uses the new image.

## Monitoring

Watch these (details in [Operations → Monitoring](../operations/monitoring.md)):

| Signal | Healthy | Alert when |
|---|---|---|
| **Snapshot age** | < 1× cadence | > 2× cadence (builds are failing or the timer isn't firing) |
| **Snapshot status** | `published` | repeated `failed` |
| **Prefix count** | stable-ish day to day | wild swings (may indicate a route leak — the sanity gate should catch these) |
| **Builder run duration** | consistent | growing sharply (data-size or resource issue) |

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `RIS bview too old` | Download failed or cache stale | Check outbound HTTPS to `data.ris.ripe.net`; delete the cached file to force a fresh fetch |
| Build **held for approval** | Prefix count swung past `SANITY_MAX_DELTA_PCT` | Investigate the swing (real routing change vs leak); approve, or set `SANITY_FORCE=true` only if you're sure |
| `GeoIP … not found` | GeoLite2 not present or key missing | Confirm the `geoipupdate` container ran and the mount path matches `VAPN_GEOIP_CITY_MMDB` |
| `configuration errors: VAPN_…` | A required variable is unset | The builder lists exactly which — set them |
| Snapshot `failed` repeatedly | Any pipeline step erroring | Read the builder logs; the failing stage is named. Previous published snapshot stays in force meanwhile |
| Signature/verify errors on workers | Signing key ≠ pinned public key | Ensure the worker image's pinned public key matches `VAPN_SNAPSHOT_SIGNING_KEY` |

## Recovery

- **A bad snapshot got published.** Roll back instantly:
  `./bin/vapnctl snapshots rollback <good-version>` (audited). Workers switch on
  their next heartbeat.
- **The builder can't run for a while.** No emergency — workers keep using the
  last published snapshot indefinitely. Fix the cause and let the next run
  publish. Routing membership changes slowly.
- **Signing key compromised.** Generate a new key, update the worker image's
  pinned public key (a worker release), and rotate. The old key's snapshots
  remain verifiable until you retire them.

For the full design and rationale, return to the
[builder overview](README.md).
