# Monitoring

The monitoring profile ships Prometheus (30-day retention) and Grafana with
the **VAPN Fleet** dashboard pre-provisioned:

```sh
cd /opt/vapn/deploy/prod
docker compose --profile monitoring up -d
# Grafana: https://$VAPN_DOMAIN/grafana/  (admin / $VAPN_GRAFANA_PASSWORD)
```

`/metrics` on each service is internal-network only — the edge never proxies
it. Alert rules live in `deploy/prod/monitoring/alerts.yml`; wire them to an
Alertmanager or recreate them in Grafana alerting — the expressions are the
contract, and each carries a runbook anchor into [runbooks.md](runbooks.md).

## Metrics catalog

Gauges are computed from the database at scrape time (a collector in the
coordinator), so they are correct even across restarts. Counters are
process-local and reset on restart — alert on `rate()`, not absolute values.

| Metric | Type | Meaning |
|---|---|---|
| `vapn_workers{state}` | gauge | Workers by lifecycle state |
| `vapn_assignments_active` | gauge | Assignments open or leased |
| `vapn_leases_live` | gauge | Unreleased leases |
| `vapn_outbox_queued` | gauge | Un-acked publication outbox rows |
| `vapn_snapshot_age_seconds` | gauge | Age of the published routing snapshot |
| `vapn_snapshot_targets` | gauge | Probe targets in the published snapshot |
| `vapn_observations_ingested_total` | counter | Accepted signed observations |
| `vapn_observations_rejected_total` | counter | Rejected observations |
| `vapn_lease_requests_total` | counter | Lease calls served |
| `vapn_trust_events_total{type}` | counter | Security/trust events |
| `vapn_consensus_windows_total` | counter | Consensus windows computed (aggregator) |
| `vapn_outbox_push_total{kind,outcome}` | counter | Pushes to VPS Advisor (aggregator) |
| `vapn_http_requests_total{route,code}` | counter | HTTP requests by route pattern |
| `vapn_http_request_duration_seconds` | histogram | Request latency by route |

The builder is a one-shot job and is monitored *indirectly and reliably*
through `vapn_snapshot_age_seconds` (the outcome that matters) plus the
systemd unit result (`systemctl status vapn-builder.service`; exit 2 =
sanity gate held the snapshot, exit 1 = failure).

## What "normal" looks like

For a fleet of N active workers with the default 30–120 s probe intervals:
observations ingest at roughly N × (assignments/worker) / interval — tens
per second at a few hundred workers; outbox queued is 0 or single digits;
snapshot age saws between 0 and ~8 h; trust events tick at ~0 with
occasional isolated spikes when a worker misbehaves.
