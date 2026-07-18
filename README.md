# VAPN — VPS Advisor Probe Network

The distributed network observability backend for [VPS Advisor]:
community-operated worker nodes measure the public network health of VPS
providers listed on VPS Advisor; trust-weighted aggregated results flow back
to the website. This repository is **not** the VPS Advisor website — see
`CLAUDE.md` for the full project brief.

```
VPS Advisor  ──catalog/enrollment──►  VAPN platform  ──assignments──►  community workers
     ▲                                                                        │
     └──────────── aggregated verdicts, anomalies, telemetry ◄────signed obs──┘
```

## Status

**All 11 phases complete** — architecture, routing snapshot builder (RIPE
RIS → signed SQLite artifacts), snapshot distribution, worker framework,
probe engine, authentication & trust, scheduler & assignments, aggregation
engine, VPS Advisor integration, administration & operations, production
readiness. See [docs/demos/](docs/demos/) for a runnable walkthrough of each
phase and [docs/architecture/](docs/architecture/) for the design.

Repository: `github.com/HummingByteDev/vpsa-network-discovery`
(private until the [launch checklist](docs/operations/launch-checklist.md) passes).

## Run a worker (community)

```sh
curl -fsSL https://install.vpsadvisor.com | bash
```

Then `vapn status` · `vapn pause` · `vapn update` · `vapn uninstall` —
Docker stays an implementation detail. Details: [docs/worker/](docs/worker/README.md).

## Run the platform (operators)

Single Ubuntu VM + Docker Compose + Caddy is the supported v1 deployment:
[deployment guide](docs/operations/deployment.md). Day-2 operations:
[monitoring](docs/operations/monitoring.md) ·
[runbooks](docs/operations/runbooks.md) ·
[backup & DR](docs/operations/backup-restore.md) ·
[upgrades](docs/operations/upgrades.md) ·
[security](docs/operations/security-hardening.md) ·
[releases](docs/operations/release-management.md).
Administration: `vapnctl` (fleet status, worker lifecycle, snapshot
rollback, kill switch, audit).

## Website team

[The integration guide](docs/integration/vpsadvisor-integration-guide.md)
is the complete contract: models, endpoints, dashboards, permissions, jobs,
rollout order. The `mockadvisor` stub in this repo implements it, so the
guide is executable.

## Layout

| Path | Contents |
|---|---|
| `docs/` | architecture · demos · integration · operations · worker |
| `cmd/` | `coordinator` `aggregator` `builder` `worker` `migrate` `vapn` `vapnctl` `loadtest` `keygen` `mockadvisor` |
| `internal/` | implementation packages (each carries its own package doc) |
| `migrations/` | PostgreSQL schemas: routing, registry, scheduling, measurements, aggregation, audit |
| `deploy/` | `compose/` dev stack · `prod/` production stack · `worker/` installer + systemd units |

## Development

```sh
make dev-up      # full stack: postgres :5433, minio, mockadvisor, coordinator,
                 #   aggregator, 3 workers (snapshot keys via ./bin/keygen — see demos)
make test-db     # one-time: create the vapn_test database in the dev postgres
make check       # vet + tests (against vapn_test, never the live dev db) + build
```

Guided tour: [docs/demos/phase1.md](docs/demos/phase1.md) onwards.
