# Phase Demos

VAPN was built in eleven phases. Each demo is a **runnable walkthrough** of what
that phase added: commands you can paste against the development stack, with the
output to expect. Together they double as a build log and an acceptance record.

These are development-history documents. For how the system works *today*, use
the [Architecture](../architecture/README.md),
[Builder](../builder/README.md), [Worker](../worker/README.md),
[Operations](../operations/README.md) and [Reference](../reference/README.md)
sections — those are kept current; these capture a moment.

Most demos assume the dev stack is up:

```sh
make dev-up      # postgres :5433, minio, mockadvisor, coordinator, aggregator, 3 workers
make test-db     # one-time: create the vapn_test database
```

See [Development](../development/README.md) for the full local setup.

| Phase | Demo | What it added |
|---|---|---|
| 1 | [Foundation](phase1.md) | Monorepo scaffold, shared platform packages, PostgreSQL schemas + migration runner, the mock VPS Advisor stub, dev compose |
| 2 | [Routing snapshot builder](phase2.md) | The full build pipeline: ASN sync, MRT parsing, prefix extraction, bogon/MOAS validation, GeoIP enrichment, versioned load, target derivation |
| 3 | [Snapshot distribution](phase3.md) | SQLite artifact export, Ed25519-signed manifests, artifact store upload, the `current` pointer, pruning and rollback |
| 4 | [Worker framework](phase4.md) | The worker agent (keypair, enrollment, heartbeat, verified snapshot sync, `doctor`), the coordinator's worker API, the admin surface |
| 5 | [Probe engine](phase5.md) | Protocol-agnostic probing with ICMP first, the measurement executor, signed observations, batched idempotent upload and intake |
| 6 | [Authentication & trust](phase6.md) | Enforced replay protection, key rotation (voluntary and admin-demanded), trust events, continuous trust scoring, the audit log |
| 7 | [Scheduler & assignments](phase7.md) | Assignments generated from published targets, diversity rules, automatic draining, probe-policy bounds |
| 8 | [Aggregation engine](phase8.md) | Trust-weighted consensus windows, provider status rollups, anomaly detection, the agreement→trust feedback loop, the publication outbox |
| 9 | [VPS Advisor integration](phase9.md) | Continuous two-way sync with the website, plus the complete Django implementation guide |
| 10 | [Administration & operations](phase10.md) | `vapnctl`, Prometheus instrumentation, alert rules, the Grafana dashboard, the production deployment, backup/restore, runbooks |
| 11 | [Production readiness](phase11.md) | The `vapn` CLI and installer, the release pipeline, a measured load ceiling, the security review, the launch checklist |
