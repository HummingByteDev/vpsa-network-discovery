# 09 — Phased Implementation Roadmap

Refines the brief's suggested phases. Every milestone ends with a functional, testable
subsystem and its documentation. Dependencies are strictly ordered top-to-bottom except
where noted; phases 4–6 interleave in practice.

## Phase 1 — Architecture & Foundation ✅(this document set) + scaffolding
**Deliver:** approved architecture; monorepo scaffold (`cmd/`, `internal/`, lint, CI);
migration tooling + empty schemas; dev compose (postgres, minio); **mock VPS Advisor
stub** serving Provider API from fixtures; shared platform packages (config, logging,
metrics, DB access).
**Test gate:** `docker compose up` yields healthy postgres + stub; migrations apply
cleanly; CI green.

## Phase 2 — Routing Snapshot Builder
**Deliver:** ASN sync from stub; MRT parse of `data/ripe/latest-bview.gz`; prefix
extraction/dedupe/validation (bogon, MOAS flags); GeoIP enrichment from `data/geo-data`;
`routing` schema population; probe-target derivation; snapshot versioning + sanity gate.
**Test gate:** golden-file tests on real bview subset; builder run on the full dev data
produces a published snapshot in Postgres with plausible counts for a fixture ASN list.

## Phase 3 — Snapshot Distribution
**Deliver:** SQLite artifact export; signed manifest; upload to artifact store; `current`
pointer + rollback; retention pruning; builder GeoLite2 auto-refresh from MaxMind
(deployer's own license key per R8 decision — no redistribution).
**Test gate:** artifact downloads, signature verifies, tamper test fails closed;
publish→supersede→rollback exercised.

## Phase 4 — Worker Framework (no probing yet)
**Deliver:** worker binary + container; keypair generation; enrollment/registration
against coordinator (built here in skeleton form: register, heartbeat, artifact
advertisement); artifact download/verify/atomic swap; config push; graceful shutdown;
`worker doctor`.
**Test gate:** compose brings up 3 workers that enroll, get approved via stub admin
endpoint, heartbeat, and converge on the current snapshot.

## Phase 5 — Probe Engine
**Deliver:** `Prober` interface; ICMP implementation (IPv4+IPv6, CAP_NET_RAW handling);
local execution loop with intervals/jitter; observation batching, signing, bounded disk
queue; upload endpoint persisting to partitioned `measurements`.
**Test gate:** end-to-end: assignment fixture → probes against lab targets → signed
batches land in Postgres; loss/latency values sane vs `ping` baseline.

## Phase 6 — Authentication & Trust (hardening pass)
**Deliver:** full Ed25519 request signing + replay protection (until now dev-mode auth);
key rotation (voluntary + demanded); worker states enforced end-to-end; trust score
skeleton (availability + tenure components); TrustEvents; audit schema wired.
**Test gate:** security test suite — replays rejected, expired timestamps rejected,
suspended worker locked out within one heartbeat, rotation overlap works, all events
audited.

## Phase 7 — Scheduler & Assignments
**Deliver:** target → assignment generation (priority, interval policy); redundancy
groups with operator/ASN diversity; lease issue/renew/expire/rebalance; drain on
snapshot supersede and provider delist; ProbePolicy enforcement; self-network exclusion.
**Test gate:** simulation test: 20 fake workers churn (join/leave/crash) against 50
fixture providers — every enabled target keeps ≥N healthy coverage, no worker probes
its own ASN, rate caps hold.

## Phase 8 — Aggregation Engine
**Deliver:** windowing pipeline; trust-weighted consensus (reachability vote, robust RTT
stats); verdict + confidence rules incl. `insufficient_data`; consensus-agreement trust
component (closing the loop with Phase 6); v1 anomaly detection (reachability loss,
latency regression, snapshot-diff routing churn); rollups + retention.
**Test gate:** replay-based tests: recorded observation sets with injected liars/outages
produce correct verdicts and demote liars' trust; property tests on aggregation
determinism.

## Phase 9 — VPS Advisor Integration
**Deliver:** publisher (outbox → Results API) with retries/idempotency; provider sync
switched from stub to real contract; enrollment/decision sync; fleet telemetry push;
**the Integration Guide** — complete site-team documentation (endpoints A1–A4 with full
schemas, DB models, dashboard/admin pages, permissions, jobs, notifications, deployment
notes) detailed enough for independent implementation.
**Test gate:** contract tests run identically against stub and (when available) staging
site; guide reviewed against contract tests.

## Phase 10 — Administration & Operations
**Deliver:** platform admin API + minimal CLI (worker states, snapshot promote/rollback,
policy edits, audit query); Prometheus metrics completed + alert rules; runbooks
(snapshot failure, worker flood, outbox backlog, compromise response); backup/restore
procedure tested.
**Test gate:** game-day exercise: kill coordinator mid-upload (no data loss), restore DB
from backup, force-rotate a worker, roll back a snapshot — all via documented runbooks.

## Phase 11 — Production Readiness
**Deliver:** load test (target: 500 simulated workers sustained, headroom measured);
security review vs threat matrix; multi-arch images + release pipeline + versioning
policy; complete doc set (architecture refresh, installation, builder, worker, API
reference, security, operations); K8s manifests; launch checklist incl. anchor-worker
rollout plan (R3) and probe-policy sign-off (R2).
**Test gate:** a person outside the project stands up the platform and enrolls a worker
using only the docs.

## Cross-phase rules

- Documentation lands in the same phase as its feature, not deferred to Phase 11
  (Phase 11 only completes and polishes).
- The mock VPS Advisor stub is maintained continuously — it is the contract's executable
  form and the reason phases 2–8 never block on the website team (risk R4).
- Each phase ends with a tagged release and a demo script in `docs/demos/`.
