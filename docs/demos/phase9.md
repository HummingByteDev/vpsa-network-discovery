# Phase 9 Demo — VPS Advisor Integration

What exists: the platform now talks to VPS Advisor in both directions —
continuously, not just at build time — and the website team has a complete
implementation guide.

- **Publisher** (aggregator): drains the publication outbox to the Results
  API every 15 s with at-least-once delivery and exponential backoff (cap
  5 min); enqueues a fleet-telemetry document every 5 min (worker states,
  versions, published snapshot, security-event counts).
- **Continuous sync** (coordinator, every 2 min): provider catalog (an
  opt-out on the website drains assignments within one scheduler pass),
  pending enrollments (workers created on the website are provisioned here
  with the *website's* worker ID and token hash — identities align across
  systems), and admin decisions (approve/suspend/quarantine/retire made on
  the website dashboard are applied idempotently).
- **Integration Guide**:
  [docs/integration/vpsadvisor-integration-guide.md](../integration/vpsadvisor-integration-guide.md)
  — models, endpoints with schemas, dashboard pages, permissions, jobs,
  notifications, rollout order. The mock stub implements this exact contract,
  so the guide is executable: implement it and integration is a config change.

## Dev flow

The dev compose coordinator/aggregator point at the mockadvisor stub:

```sh
docker logs cnip-dev-aggregator-1 2>&1 | grep "published to VPS Advisor" | tail -3
docker logs cnip-dev-mockadvisor-1 2>&1 | grep "received provider status" | tail -3
docker exec cnip-dev-postgres-1 psql -U cnip -d cnip -c \
  "select kind, count(*) filter (where acked_at is not null) acked,
          count(*) filter (where acked_at is null) queued
   from aggregation.publication_outbox group by 1;"
```

## Tests

- `internal/publisher`: advisor down → row survives with backoff and is not
  retried early; advisor recovers → pushed exactly once and acked. Telemetry
  document shape.
- `internal/coordinator/TestAdvisorSyncFlow`: fixture enrollment + suspend
  decision from the stub → worker provisioned under the advisor's ID, token
  redeemable, decision applied, second pass idempotent. (Also fixed en route:
  admins can now suspend a still-pending worker.)
