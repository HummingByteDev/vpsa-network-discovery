# VAPN Operations

Operator documentation for running the platform side of VAPN (coordinator,
aggregator, builder, database, edge). For the community worker experience see
[docs/worker/](../worker/); for architecture see [docs/architecture/](../architecture/).

| Document | Covers |
|---|---|
| [deployment.md](deployment.md) | Production install: single VM, Docker Compose, Caddy |
| [monitoring.md](monitoring.md) | Metrics catalog, Grafana dashboard, alert rules |
| [runbooks.md](runbooks.md) | What to do when each alert fires |
| [backup-restore.md](backup-restore.md) | Backups, restore drill, disaster recovery |
| [upgrades.md](upgrades.md) | Upgrading and rolling back the platform |
| [security-hardening.md](security-hardening.md) | Host and stack hardening, compromise response |
| [release-management.md](release-management.md) | Versioning, images, publishing a release |
| [load-testing.md](load-testing.md) | The 500-worker load harness and expected numbers |
| [launch-checklist.md](launch-checklist.md) | Everything to verify before going public |

The administrative command line is `vapnctl` (see `vapnctl --help`):
fleet status, worker lifecycle, snapshot rollback, scheduler kill switch,
audit queries — all against the coordinator admin API.
