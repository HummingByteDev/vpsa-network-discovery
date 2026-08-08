# Phase 11 Demo — Production Readiness & the Contributor Experience

What exists: the worker is now a product. Installing one feels like
installing Tailscale, not deploying OpenStack — and the platform ships with
a release pipeline, a measured load ceiling, a security review, and a launch
checklist.

## The `vapn` CLI (cmd/vapn)

Docker is an implementation detail; contributors get a stable UX:

```
# deploy/worker/install.sh
curl -fsSL https://raw.githubusercontent.com/HummingByteDev/vpsa-network-discovery/main/deploy/worker/install.sh | bash
```

```
System check
✓ Docker CLI            ✓ State directory writable
✓ Docker daemon         ✓ Snapshot public key
✓ Docker Compose        ✓ Coordinator reachable
✓ Disk space (≥2 GB)    ✓ Clock synchronization

Starting worker
✓ Registration successful

Worker ID:  0ededc0e-fab2-4691-8790-c6ac897494aa
Status:     Awaiting approval
```

```
$ vapn status
Worker:                 Healthy
Routing snapshot:       20260630T0000Z-1784390550789
Last heartbeat:         9s ago
Assignments:            64
Measurements submitted: 116
Last upload:            39s ago (33 ms)
```

`vapn pause|resume|logs|doctor|backup|update|unregister|uninstall` complete
the lifecycle. Updates are health-gated: pull → recreate → wait for a fresh
`status.json` (the container↔CLI contract, written after every heartbeat) →
automatic rollback to the previous image if the worker doesn't report
healthy. `deploy/worker/vapn-update.timer` makes that automatic and boring.

**Live-verified end to end** against the dev stack: enrollment token from
`vapnctl workers create` → install → approve → probing real targets within
two minutes → pause/resume → graceful update handling → backup →
`unregister` (worker shows `retired (operator requested)` server-side) →
`uninstall` leaves nothing behind.

New protocol surface: `POST /api/v1/workers/retire` (signed) — operators can
leave without admin involvement; `status.json` self-reporting.

## Load test (cmd/loadtest)

500 simulated workers doing the full signed protocol (register, heartbeat,
lease, upload) against one coordinator + postgres on a 4-core laptop:

```
=== loadtest: 500 workers, 3m0s steady state ===
registered: 500/500

op               ok   errors        p50        p95        max
register        500        0       11ms       34ms      335ms
heartbeat      2500        0        6ms        8ms       38ms
lease          1547        0       13ms       21ms       94ms
upload          506        0       15ms       24ms       43ms
```

Coordinator: ~9% CPU, 15 MB RSS during the run. The v1 target (several
hundred workers on a modest VM) holds with an order of magnitude of
latency headroom.

## Release pipeline

Tagging `vX.Y.Z` builds multi-arch (amd64/arm64) images for all six
components to GHCR and attaches `vapn`/`vapnctl` binaries + `install.sh` +
checksums to the GitHub release (`.github/workflows/release.yml`).
Policy: [docs/operations/releases-and-upgrades.md](../operations/releases-and-upgrades.md).

## Paperwork that isn't paperwork

- [Security threat-matrix verification](../operations/security.md#threat-matrix-verification): all 16 threats from
  the threat matrix traced to mitigations and their tests.
- [Load testing](../operations/monitoring.md#load-testing), [launch checklist](../operations/launch-checklist.md),
  [worker guide](../worker/README.md).

## Also in this phase

Local `make test` now targets a dedicated `vapn_test` database
(`make test-db` creates it) after this phase's live run demonstrated why:
the suite's truncates had wiped the live dev stack's routing data —
recovered, satisfyingly, with `vapnctl snapshots rollback` + one forced
builder run, exactly as the runbooks prescribe.
