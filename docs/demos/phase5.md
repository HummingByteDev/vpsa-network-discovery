# Phase 5 Demo — Probe Engine

What exists: the protocol-agnostic probe engine with ICMP as its first
implementation, the worker's measurement executor (leased assignments →
interval probing with jitter → individually signed observations → batched
idempotent uploads), and the coordinator's observation intake (per-observation
signature verification, held-assignment validation, COPY insert into the
partitioned measurements schema). Scheduling here is deliberately minimal
(greedy claim up to capacity); the real scheduler with redundancy groups and
diversity replaces it in Phase 7 behind the same endpoint.

## Probe engine design

`internal/probe` defines `Prober` — `Type()` + `Probe(ctx, target, params)` →
`{OK, median RTT, sent/lost, metrics}`. New protocols (TCP connect, HTTP)
implement the same interface and need zero changes to scheduling, upload, or
schemas (`probe_type` + metrics JSONB travel end-to-end). ICMP prefers
unprivileged datagram sockets (`net.ipv4.ping_group_range` sysctl, set in the
dev compose) and falls back to raw sockets (`CAP_NET_RAW`).

Safety property: the executor refuses any assignment whose target is not
listed in the worker's **verified** snapshot artifact — a compromised
coordinator cannot point workers at arbitrary hosts.

## 1. Seed assignments (dev)

Assignments normally come from the scheduler (Phase 7). For now, create them
from published probe targets:

```sh
docker exec vapn-dev-postgres-1 psql -U vapn -d vapn -c "
insert into scheduling.assignment (target_id, provider_id, probe_type, interval_seconds, redundancy_group)
select t.id, t.provider_id, 'icmp', 15, gen_random_uuid()
from routing.probe_target t
join routing.snapshot s on s.id = t.snapshot_id and s.status = 'published'
limit 12;"
```

## 2. Watch measurements arrive

Workers lease within 60 s, probe every 15 s, flush every 30 s:

```sh
docker exec vapn-dev-postgres-1 psql -U vapn -d vapn -c "
select p.name, count(*) observations,
       round(avg(o.rtt_ms)::numeric, 1) avg_rtt_ms,
       round((sum(o.packets_lost)::numeric / nullif(sum(o.packets_sent),0)) * 100, 2) loss_pct,
       count(distinct o.worker_id) workers
from measurements.observation o
join routing.provider p on p.provider_id = o.provider_id
group by 1 order by 2 desc;"
```

Every row carries the worker's Ed25519 signature over the canonical
observation JSON — provenance survives aggregation and audits.

## 3. Intake defenses (tested)

`internal/coordinator/measurement_test.go`: a batch containing a valid
observation, a tampered one (field altered after signing), one signed with a
foreign key, and one for an assignment the worker doesn't hold results in
exactly 1 accepted row; re-uploading the same `batch_id` inserts nothing
(idempotent). `TestProbeToDatabase` runs the whole loop with a fake prober:
seeded assignment → executor → signed batch → verified rows.

## 4. Notes

- ProbePolicy rate caps and per-target budgets land with the real scheduler
  (Phase 7); intervals are enforced by assignment until then.
- Observation retention: raw partitions are pruned after N days once rollups
  exist (Phase 8); the DEFAULT partition carries dev volumes fine.
- **Target responsiveness** (observed live): "first usable address" targets
  often don't answer ICMP — the reference run against real Hetzner prefixes
  showed ~80% packet loss across targets while responsive addresses answered
  consistently at ~25 ms. Planned refinement (Phase 7/8): score target
  responsiveness and bias assignment toward addresses that demonstrably
  reply, keeping unresponsive-but-announced prefixes as reachability signals
  only — aggregation must never read "target ignores ping" as "provider
  outage" (the conservative `insufficient_data` posture covers this).
