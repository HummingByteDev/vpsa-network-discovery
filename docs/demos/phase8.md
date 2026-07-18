# Phase 8 Demo — Aggregation Engine

What exists: the consensus pipeline. Raw signed observations become
trust-weighted consensus windows; windows roll up into public provider status;
transitions open/resolve anomalies; per-worker consensus agreement feeds back
into trust (closing the Phase 6 loop); status documents queue in the
publication outbox (drained to VPS Advisor in Phase 9); retention prunes raw
data.

## Consensus model

Per 5-minute window (60 s in dev), per provider:

1. Each worker's observations per target reduce to an ok-ratio; workers vote
   with their trust weight (floor 0.1 so new workers count a little).
2. A target is **up** if ≥50% of voting weight saw it reachable.
3. Only **responsive targets** — those that answered *someone* in the trailing
   24 h — count toward the verdict. An address that never answers ICMP is a
   non-signal, never an outage (the target-responsiveness lesson from the
   Phase 5 live run, baked into the math).
4. Verdict over responsive targets measured this window:
   ≥90% up → `healthy`, ≥50% → `degraded`, else `outage`; fewer than
   `CNIP_MIN_WORKERS` distinct workers (default 3) → `insufficient_data`,
   confidence 0 — the platform never guesses.
5. Confidence = worker-diversity factor × (1 − dissent); dissent is how split
   the weighted vote was.

Status rollup keeps `since` stable across unchanged verdicts, opens a
`reachability_loss` anomaly on transition into outage/degraded, resolves it
on return to healthy, and flags `latency_regression` when window p50 doubles
against the trailing 6-hour baseline (auto-resolves on return to <1.2×).

## Trust feedback

Each settled window writes `aggregation.worker_agreement`: a worker's mean
closeness to the settled per-target consensus. The scorer now computes

```
score = clamp( availability×(0.2 + 0.3×tenure) + 0.5×agreement − penalty )
```

with agreement (24 h mean, neutral 0.5 with no history) as the dominant term.
Scoring against *settled* windows means a worker that's right early in an
outage isn't punished by the instantaneous majority.

## Watch it live

```sh
docker exec cnip-dev-postgres-1 psql -U cnip -d cnip -c "
select p.name, s.verdict, round(s.confidence::numeric,2) conf, s.since, s.metrics
from aggregation.provider_status s join routing.provider p using (provider_id);

select provider_id, kind, severity, opened_at, resolved_at
from aggregation.anomaly order by opened_at desc limit 5;

select w.name, round(t.score::numeric,3) trust, t.components
from registry.trust_score t join registry.worker w on w.id = t.worker_id;

select kind, count(*) filter (where acked_at is null) queued
from aggregation.publication_outbox group by 1;"
```

## Replay test suite

`internal/aggregate`: three honest workers + one liar → verdict stays
healthy, liar's agreement <0.5 and trust drops below honest workers';
fleet-wide loss of a previously-responsive target → `outage` + anomaly
opened, recovery → `healthy` + anomaly resolved + outbox populated; single
worker → `insufficient_data`/0-confidence; window recomputation is
idempotent. (DB test packages share one database — run with `-p 1`, as
`make test` and CI do.)
