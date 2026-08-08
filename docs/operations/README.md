# Operations

> Part of the [VAPN documentation](../README.md). New to the system? Start with
> [Architecture](../architecture/01-system-architecture.md) and the
> [end-to-end walkthrough](../walkthroughs/end-to-end.md).

Operator documentation for running the platform side of VAPN — coordinator,
aggregator, builder, database, edge. For the community worker experience see
[Community Workers](../worker/README.md).

**Installing for the first time?** → **[Install the builder](../builder/installation.md)**
is the step-by-step guide from a fresh VPS to a published signed snapshot. It
brings up the whole platform. Everything here assumes it's already running.

| Document | Covers |
|---|---|
| [deployment.md](deployment.md) | The production topology: what talks to what, compose profiles, scheduled jobs, scaling, first workers |
| [monitoring.md](monitoring.md) | Metrics catalog, Grafana dashboard, alert rules, load testing |
| [runbooks.md](runbooks.md) | What to do when each alert fires |
| [backup-restore.md](backup-restore.md) | Backups, restore drill, disaster recovery |
| [releases-and-upgrades.md](releases-and-upgrades.md) | Versioning, cutting a release, upgrading, rolling back software and snapshots |
| [security.md](security.md) | Host and stack hardening, threat-matrix verification, compromise response |
| [launch-checklist.md](launch-checklist.md) | Everything to verify before going public |

The administrative command line is `vapnctl` — fleet status, worker lifecycle,
snapshot rollback, scheduler kill switch, audit queries, all against the
coordinator admin API. Full command list: [CLI reference](../reference/cli.md#vapnctl-platform-administration).
