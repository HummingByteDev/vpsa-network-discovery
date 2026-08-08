# 01 — System Architecture

## 1. Purpose

Build the distributed network-intelligence backend that measures the **public network
health of VPS providers** listed on VPS Advisor, using community-operated worker nodes,
and delivers trust-weighted aggregated results back to the VPS Advisor website.

Non-goals: provider profiles, reviews, accounts, billing, any website frontend, and any
form of Internet-wide scanning or provider discovery. VPS Advisor is the sole source of
truth for *which* providers exist and *which* ASNs they own.

## 2. API plane split (clarification of the brief)

The brief describes worker endpoints (registration, assignments, uploads) as living "on
the VPS Advisor website", while also making this repository responsible for probe
scheduling, worker management, and APIs. We resolve this deliberately:

- **Control plane — VPS Advisor (existing site, extended).** Human-facing and
  low-volume: worker operator accounts, worker enrollment & approval, provider catalog
  API, monitoring configuration, admin dashboard, and the Results API that ingests
  aggregated status. These endpoints are specified in [04-api-contracts.md](04-api-contracts.md)
  for the website team to implement.
- **Data plane — this platform (Coordinator API).** Machine-facing and high-volume:
  worker heartbeats, assignment leases, observation uploads, snapshot artifact
  download. Thousands of workers probing at second-to-minute intervals must not hit the
  production marketing/review website.

Rationale: volume isolation, independent scaling/deployment, and a much smaller change
surface on the existing website. Workers are *logically* "registered with VPS Advisor"
(operator accounts and approval live there) but *operationally* talk to the Coordinator.
Credentials issued during enrollment on VPS Advisor are honoured by the Coordinator via a
shared trust anchor (see [05-security-trust-model.md](05-security-trust-model.md)).

## 3. Component overview

```
                        ┌──────────────────────────────┐
                        │      VPS Advisor Website      │  (existing, extended)
                        │  provider catalog · operator  │
                        │  accounts · enrollment · admin│
                        │  dashboard · Results API      │
                        └──────┬─────────────▲──────────┘
               Provider/Config │             │ aggregated status,
               API (pull)      │             │ worker telemetry (push)
                               ▼             │
┌──────────────────────────────────────────────────────────────────┐
│              VPS Advisor Probe Network (VAPN)              │
│                                                                   │
│  ┌──────────────┐   ┌──────────────┐   ┌───────────────────────┐ │
│  │  Snapshot    │   │ Coordinator  │   │  Aggregation Engine    │ │
│  │  Builder     │   │  API +       │   │  (consensus, health,   │ │
│  │  (RIS+GeoIP) │   │  Scheduler   │   │   anomaly detection)   │ │
│  └──────┬───────┘   └──▲────────┬──┘   └──────────▲────────────┘ │
│         │ publishes    │        │ assignments      │ observations │
│         ▼              │        ▼                  │              │
│  ┌──────────────┐      │   ┌─────────────────────────────┐       │
│  │ Artifact     │◄─────┼───┤        PostgreSQL            │       │
│  │ Store (HTTP) │ dl   │   │ routing · registry · sched · │       │
│  └──────▲───────┘      │   │ measurements · aggregation   │       │
│         │              │   └─────────────────────────────┘       │
└─────────┼──────────────┼─────────────────────────────────────────┘
          │ snapshots    │ signed requests
          │              │ (heartbeat, lease, upload)
      ┌───┴──────────────┴───┐
      │   Worker Network      │  community Docker containers
      │  (probe engine, agent)│
      └───────────────────────┘
```

## 4. Components, boundaries, responsibilities

### 4.1 Snapshot Builder (`builder`)
A batch service (scheduled job, not a daemon) that owns **routing intelligence**.

Responsibilities:
- Pull the monitored-ASN list from VPS Advisor's Provider API (never maintain its own
  provider registry; the platform stores only ASN/prefix data needed for routing).
- Download RIPE RIS MRT `bview` dumps (dev shortcut: `data/ripe/latest-bview.gz` is
  pre-downloaded).
- Parse MRT, extract only prefixes originated by monitored ASNs, deduplicate, and
  validate origin (drop obviously bogus announcements: bogons, absurd prefix lengths,
  multi-origin conflicts flagged for review).
- Enrich with MaxMind GeoIP (country/city/registered ASN), fetched from MaxMind with
  the platform operator's own licence key (never redistributed); GeoIP refresh is an independent
  job on its own cadence.
- Load the result into the canonical `routing` schema in PostgreSQL, versioned.
- Export a compact, signed **SQLite artifact** per snapshot version plus a metadata
  manifest (version, counts, checksums, signature, min compatible worker version) and
  publish both to the Artifact Store.

Boundary: the builder is the *only* component that touches MRT files or MaxMind
databases. Workers and the coordinator consume finished artifacts/tables only.

### 4.2 Coordinator (`coordinator`)
A long-running HTTP service — the single endpoint workers talk to.

Responsibilities:
- Worker authentication (Ed25519 signature verification, replay protection).
- Worker lifecycle enforcement (pending → active → suspended/quarantined/retired).
- Heartbeats, config push, software-version advertisement.
- **Scheduler** (embedded module): converts monitoring targets (provider × ASN ×
  representative prefixes × region × probe type × interval) into assignments, leases
  them to workers with a redundancy factor (each target measured by N workers across
  distinct regions/networks), rebalances on worker churn.
- Observation intake: validates, timestamps, and persists signed observations to the
  `measurements` schema; never publishes them directly.
- Serves artifact metadata and redirects/streams snapshot downloads.
- Emits security/audit events.

Boundary: the coordinator never computes public status (aggregation's job) and never
decides *which providers* are monitored (VPS Advisor's job — it only syncs that list).

### 4.3 Aggregation Engine (`aggregator`)
A long-running service (or scheduled pipeline) that owns **consensus and public truth**.

Responsibilities:
- Window observations (e.g. 1-min raw → 5-min consensus → hourly/daily rollups).
- Trust-weighted consensus per provider, per region, per protocol: health state,
  confidence score, latency percentiles, packet loss.
- Anomaly detection (routing instability signals, sudden reachability loss, latency
  regressions) — v1 statistical thresholds, extensible later.
- Feed the trust engine: score each worker's agreement with consensus (see 05).
- Push aggregated results to VPS Advisor's Results API; retry with outbox semantics.

Boundary: only aggregated, consensus-backed data leaves the platform. A single worker's
observation is never public.

### 4.4 Worker (`worker`)
A community-run Docker container with minimal configuration (enrollment token + endpoint
URL; everything else is automatic).

Responsibilities: generate keypair, register, poll for approval, download & verify
routing snapshot (no MaxMind key needed — geo attribution is done centrally by the
builder and ships as derived fields inside the artifact), heartbeat, request assignment
leases, execute
probes, sign and upload observations, self-update its data artifacts, honour remote
config (rate limits, intervals, suspension).

The **probe engine** inside the worker is protocol-agnostic: a `Prober` interface
(`Probe(target, params) → Observation`) with ICMP echo as the first implementation;
TCP connect, traceroute, HTTP(S) are future implementations behind the same interface —
new protocols must not require redesign of scheduling, upload, or aggregation schemas
(measurement records carry `probe_type` + typed metrics JSON).

Boundary: workers never choose targets, never parse MRT, never talk to VPS Advisor
directly (except the operator's browser during enrollment).

### 4.5 Artifact Store
Dumb, cacheable HTTPS storage (S3-compatible or nginx-fronted volume) for snapshot
artifacts. Integrity comes from signed manifests, so it needs no trust of its own
and can sit behind a CDN as the worker fleet grows.

### 4.6 PostgreSQL
Single logical database, five schemas (`routing`, `registry`, `scheduling`,
`measurements`, `aggregation`) plus `audit`. Detailed design in
[03-database-design.md](03-database-design.md). Measurements are time-partitioned;
raw observations have a bounded retention with rollups kept long-term.

## 5. Communication flows

1. **Provider sync** (coordinator ← VPS Advisor, pull, every few minutes): monitored
   providers + ASNs + priority + enabled flag. Stored as *cache with provenance*, not a
   registry — rows carry the VPS Advisor provider ID and are dropped when delisted.
2. **Snapshot build** (builder, scheduled): ASN list → RIS parse → Postgres → artifact →
   publish → coordinator advertises new version → workers download on next heartbeat.
3. **Enrollment** (operator ↔ VPS Advisor, then worker ↔ coordinator): operator creates
   worker on the site → gets one-time enrollment token → worker boots, generates keys,
   registers with token → admin approves on the site → approval propagates to
   coordinator → worker becomes active. Details in [06-lifecycles.md](06-lifecycles.md).
4. **Measurement loop** (worker ↔ coordinator): heartbeat → lease assignments → probe →
   upload signed observation batches → coordinator persists → aggregator consumes.
5. **Publication** (aggregator → VPS Advisor Results API, push): aggregated provider /
   regional status documents; worker fleet telemetry summaries for the admin dashboard.

## 6. Technology choices

| Concern | Choice | Rationale |
|---|---|---|
| Language (all services + worker) | **Go** | Single static binaries; scratch-based Docker images for community workers; strong stdlib for raw sockets/ICMP; one language across the codebase. |
| Database | **PostgreSQL 16+** | Brief mandate; partitioning for measurements, `inet`/`cidr` types for routing, LISTEN/NOTIFY for cheap intra-platform signaling. |
| Worker snapshot format | **SQLite** file | Workers need read-only lookup, zero server dependency, atomic replace-on-update. |
| MRT parsing | Existing Go BGP library (e.g. the `gobgp`/`mrt` parser family), wrapped behind our own interface | Don't hand-roll MRT. |
| Inter-service auth (platform-internal) | mTLS or network isolation (same trust zone) | Services co-deployed; workers are the hostile edge, not services. |
| Worker auth | Ed25519 request signing | See 05. |
| Packaging | Docker + Compose (dev), Compose/K8s manifests (prod) | Brief mandate. |

## 7. Scaling posture

- Coordinator is stateless → horizontal scale behind a load balancer; leases and
  replay-nonce state live in Postgres (nonce cache may move to Redis later — noted as an
  explicit future seam, not a v1 dependency).
- Measurements schema is the write hotspot → batched uploads (worker-side batching),
  COPY-style inserts, native partitioning by day, aggressive rollup + pruning.
- Artifact distribution is CDN-offloadable from day one.
- Aggregation is windowed and embarrassingly parallel by provider.

Target order of magnitude from the brief: tens of thousands of providers, thousands of
workers. The v1 design must be *correct* at hundreds of workers and *not structurally
blocked* from the target scale.
