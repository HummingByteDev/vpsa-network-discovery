# Phase 10 Demo — Administration & Operations

What exists: the platform is now operable like a mature self-hosted
infrastructure project — an admin CLI, full Prometheus instrumentation with
alert rules and a Grafana dashboard, a production deployment (single VM,
Compose, Caddy), tested backup/restore, and runbooks for every alert.

## vapnctl

Everything an operator does day-to-day, against the coordinator admin API
(`VAPN_COORDINATOR_URL` + `VAPN_ADMIN_TOKEN`):

```sh
$ vapnctl status
Workers:         active=4
Snapshot:        20260630T0000Z-1784388997144 (462 targets, published ...)
Assignments:     1386 open/leased, 0 live leases
Outbox:          0 queued
Scheduler:       running

$ vapnctl workers list | head -3
$ vapnctl workers show <id>            # trust, leases, recent trust events
$ vapnctl workers suspend <id> --reason "..."
$ vapnctl snapshots list               # marks pruned versions "ROLLBACK? pruned"
$ vapnctl snapshots rollback <version> # workers converge next heartbeat
$ vapnctl scheduler pause              # global kill switch
$ vapnctl audit --category admin --limit 20
```

New admin endpoints backing it: `GET /admin/v1/overview`,
`GET /admin/v1/workers/{id}`, `GET /admin/v1/snapshots`,
`POST /admin/v1/snapshots/{version}/rollback`, `GET /admin/v1/audit`.

## Metrics

`/metrics` (internal-only in production) now carries domain instruments —
gauges computed from the DB at scrape time (correct across restarts),
counters for flows:

```sh
curl -s localhost:8080/metrics | grep ^vapn_
# vapn_workers{state="active"} 4        vapn_snapshot_age_seconds 512
# vapn_assignments_active 1386          vapn_observations_ingested_total 344
curl -s localhost:8082/metrics | grep ^vapn_outbox_push
# vapn_outbox_push_total{kind="provider_status",outcome="ok"} 5
```

Alert rules (`deploy/prod/monitoring/alerts.yml`) cover: services down,
snapshot staleness (>18 h), outbox backlog, fleet loss, security-event
spikes, stalled data plane, 5xx ratio — each annotated with its runbook
anchor in [docs/operations/runbooks.md](../operations/runbooks.md). The
**VAPN Fleet** Grafana dashboard is provisioned automatically by the
monitoring profile.

## Production deployment

`deploy/prod/`: Compose stack (Caddy edge with auto-TLS; only 80/443
published; `/admin/v1` CIDR-allowlisted; coordinator/aggregator read-only
distroless with self-healthchecks), `.env.example` with every setting,
systemd timers for the 8-hourly snapshot build and nightly backup, geoip
profile (bring-your-own MaxMind key), monitoring profile. The builder now
downloads the RIS bview itself (`VAPN_RIS_BVIEW_URL`) with an atomic
rename, reusing a fresh-enough local copy. Guide:
[docs/operations/deployment.md](../operations/deployment.md).

## Verified in this phase (live dev stack)

- Real build: sanity gate correctly held a 226,600% swing (leftover test
  snapshot as baseline), forced per runbook, published 2,267 prefixes / 462
  targets; 3 workers + 1 sim converged.
- `vapnctl` status/list/show/snapshots/audit against the live coordinator.
- Backup → `pg_restore --list` verification → restore into a scratch
  postgres: registry and routing counts matched the source exactly
  (observations differed only by ingest continuing after the dump).
- Rollback path covered by `TestAdminSurface` (refuses pruned versions,
  flips DB status + store pointer, invalidates the manifest cache).

## Tests

`internal/coordinator/TestAdminSurface` — overview, snapshot list,
rollback + pruned-refusal, worker detail, audit query, 401 without
credential. Plus the Phase-10 hardening: the lease claim SQL now takes at
most one replica per redundancy group per call (`TestSchedulerSimulation`
exercises wholesale re-leasing after expiry).
