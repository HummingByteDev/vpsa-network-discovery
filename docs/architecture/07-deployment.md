# 07 — Deployment Architecture

## 1. Artifacts

Every deployable is a container image built from this monorepo:

| Image | Contents | Runs as |
|---|---|---|
| `cnip/builder` | snapshot builder binary | scheduled job (cron / K8s CronJob) |
| `cnip/coordinator` | coordinator API + scheduler | long-running, horizontally scalable |
| `cnip/aggregator` | aggregation engine + trust scoring + publisher | long-running (leader-elected or single) |
| `cnip/worker` | worker agent + probe engine | community-run container |
| `cnip/migrate` | migration runner | init job |

Monorepo layout (planned):

```
cmd/{builder,coordinator,aggregator,worker,migrate}/
internal/{routing,registry,scheduling,measurement,aggregation,trust,
          probe,artifact,sync,auth,platform}/   # platform = shared infra (db, log, cfg)
migrations/
deploy/{compose,k8s}/
docs/{architecture,api,operations,worker,integration}/
```

Worker image notes: FROM scratch/distroless + static Go binary; needs `CAP_NET_RAW`
(ICMP) — documented, requested explicitly in compose/run examples; unprivileged
otherwise; persistent volume only for the private key and artifact cache. Multi-arch
(amd64/arm64) because community hardware varies.

## 2. Environments

### Local development
`deploy/compose/dev.compose.yaml`: postgres, minio (artifact store), coordinator,
aggregator, one or more local workers, plus a `builder` one-shot profile. Seed script
provides a **mock VPS Advisor** (tiny stub service serving the Provider/enrollment/
Results APIs from fixtures) so the whole loop runs offline. `data/` (pre-downloaded RIS
bview + GeoLite2) is mounted into the builder — no network fetch needed in dev.

### Production (platform side)
Two supported topologies, same images:
- **Compose (single host):** everything on one VM behind a reverse proxy (TLS
  termination), managed Postgres or local volume + WAL archiving to object storage.
  Suitable for launch scale (≤ a few hundred workers).
- **Kubernetes:** coordinator Deployment (HPA), aggregator Deployment (1 replica w/
  leader election ready), builder CronJob, migrate as pre-upgrade Job/hook, Postgres
  external (managed), artifact store = S3-compatible + CDN in front for worker
  downloads.

Only the coordinator and artifact store are Internet-exposed. Aggregator and builder
have no inbound surface. Egress: coordinator/aggregator → VPS Advisor APIs; builder →
RIPE RIS + MaxMind; workers → coordinator + artifact CDN only.

### Community worker deployment
One documented command:

```yaml
# docker-compose.yml the operator downloads
services:
  worker:
    image: ghcr.io/vpsadvisor/cnip-worker:latest
    cap_add: [NET_RAW]
    environment:
      CNIP_ENROLLMENT_TOKEN: "…"        # only required setting
      CNIP_COORDINATOR_URL: "https://probe-api.vpsadvisor.example"
      # CNIP_MAXMIND_LICENSE_KEY: "…"   # optional; operator's own key (see R8)
    volumes: [worker-state:/state]
    restart: unless-stopped
volumes: { worker-state: }
```

## 3. Configuration & secrets

12-factor: env vars + optional config file; every service prints effective config
(secrets redacted) at boot. Secrets: DB DSNs per-service role, VPS Advisor service
credential, snapshot signing key (builder only — ideally injected per-run, kept
offline-capable), admin API tokens. K8s: Secrets/external-secrets; compose: env files
outside the repo.

## 4. Observability

- Structured JSON logs (zerolog/slog) everywhere; request IDs propagated.
- Prometheus metrics on every service (`/metrics`, internal port): upload rates,
  signature failures, lease backlog, consensus lag, snapshot age, outbox depth.
- Health endpoints: `/healthz` (liveness), `/readyz` (DB + dependency checks).
- Key alerts (ops docs later): snapshot age > 2× cadence, publication outbox growing,
  active worker count drop, consensus `insufficient_data` ratio rising, signature
  failure spike.

## 5. Upgrade & migration strategy

- DB migrations are backward-compatible one version back (expand→migrate→contract), so
  coordinator replicas can roll.
- Coordinator/aggregator: rolling deploy; workers tolerate coordinator downtime by
  queueing observations locally (bounded disk buffer) and retrying — brief platform
  maintenance loses no data.
- Worker protocol versioned (`/api/v1/`); min-worker-version enforcement via heartbeat
  gives a lever to sunset old workers gracefully (drain, notify operator via site).
