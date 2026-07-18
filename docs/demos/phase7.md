# Phase 7 Demo — Scheduler & Assignments

What exists: the real scheduler. Assignments are now *generated* from
published probe targets (no more manual seeding), distributed under diversity
rules, drained automatically when snapshots supersede or providers opt out,
and bounded by probe policy.

## Generation

`internal/scheduler` reconciles every `CNIP_SCHEDULER_INTERVAL` (30 s):

- One assignment **replica** per redundancy slot (`CNIP_REDUNDANCY`, default
  3) per active target of the published snapshot, provider enabled and not
  delisted.
- Redundancy groups are deterministic (`md5('cnip-group-'||target_id)::uuid`),
  making reconciliation idempotent.
- Interval policy by provider priority: ≤10 → 30 s, ≤50 → 60 s, else 120 s,
  floored at 15 s (ProbePolicy).

## Distribution (claim rules, enforced in SQL at lease time)

1. A worker never holds two replicas of the same redundancy group — so N
   replicas ⇒ N *distinct* workers per target.
2. **Self-network exclusion:** a worker whose source ASN (resolved from its
   registration IP via GeoLite2-ASN, `CNIP_GEOIP_ASN_MMDB`) belongs to a
   provider never measures that provider.
3. Capacity is clamped server-side (`CNIP_MAX_ASSIGNMENTS_PER_WORKER`, 64).
4. Expired leases are reaped fleet-wide on every lease call; crashed workers'
   assignments reopen automatically after the lease TTL.

## Drain

Snapshot superseded, target dropped, provider disabled/delisted (the opt-out
path) → assignments drain: leases released, rows closed, workers stop within
one lease interval. The scheduler recreates assignments for the new
snapshot's targets on its next pass.

## Kill switch

```sh
AUTH='Authorization: Bearer dev-admin-token'
curl -s -H "$AUTH" -X POST localhost:8080/admin/v1/scheduler/pause -i   # fleet idles
curl -s -H "$AUTH" -X POST localhost:8080/admin/v1/scheduler/resume -i
```

Paused: every lease call returns an empty set (audited), so all probing stops
within one lease interval — the global stop lever from the security design.

## Simulation gate (automated)

`TestSchedulerSimulation`: 10 providers × 5 targets × redundancy 3 (150
replicas), 20 real signed workers with capacity 12, two of them planted
inside provider 0's network. After leasing, five workers die; survivors renew
until the dead leases expire and are reclaimed. Asserts: every group covered
by ≥3 distinct live workers, no duplicate replica per worker, zero self-ASN
violations, nobody over capacity, generation idempotent, interval policy
honored. `TestDrainOnSupersede` covers the drain path.

## Watching it live

```sh
docker exec cnip-dev-postgres-1 psql -U cnip -d cnip -c "
select a.status, count(*) from scheduling.assignment a group by 1;
select count(distinct l.worker_id) workers, count(*) live_leases
from scheduling.lease l where l.released_at is null;"
```

With the dev fleet (3 workers, redundancy 3) coverage is partial by design —
462 targets × 3 replicas exceed three workers' capacity; the fleet grows, the
coverage follows. Aggregation (Phase 8) reports confidence accordingly.
