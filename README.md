# Community Network Intelligence Platform

The distributed network observability backend for [VPS Advisor]: community-operated
worker nodes measure the public network health of VPS providers listed on VPS Advisor;
trust-weighted aggregated results flow back to the website. This repository is **not**
the VPS Advisor website — see `CLAUDE.md` for the full project brief.

## Status

Phases 1–9 complete: Architecture & Foundation, Routing Snapshot Builder,
Snapshot Distribution, Worker Framework, Probe Engine, Authentication & Trust,
Scheduler & Assignments, Aggregation Engine, VPS Advisor Integration
(see [the integration guide](docs/integration/vpsadvisor-integration-guide.md)
for the website team's implementation contract). See the
[roadmap](docs/architecture/09-roadmap.md) for what comes next, and
[docs/demos/](docs/demos/) for runnable per-phase walkthroughs.

Repository: `github.com/HummingByteDev/vpsa-network-discovery`
(private until deployment checks pass).

## Layout

| Path | Contents |
|---|---|
| `docs/architecture/` | Approved architecture (start at its README) |
| `docs/demos/` | Per-phase runnable demo scripts |
| `cmd/` | Entrypoints: `builder`, `coordinator`, `aggregator`, `worker`, `migrate`, `mockadvisor` |
| `internal/platform/` | Shared packages: config, logging, db, httpserver, version |
| `internal/mockadvisor/` | Fixture-driven VPS Advisor stub (executable API contract) |
| `migrations/` | PostgreSQL schema migrations (six schemas) |
| `deploy/compose/` | Dev environment |
| `data/` | Pre-downloaded RIPE RIS bview + GeoLite2 (dev convenience; git-ignored) |

## Quick start

```sh
make dev-up     # full dev stack (postgres :5433, minio :9000, stub :8081,
                #  coordinator :8080, aggregator :8082)
make check      # vet + tests + build
```

See [docs/demos/phase1.md](docs/demos/phase1.md) for the guided tour.
