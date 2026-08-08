# 03 — Database Design (PostgreSQL)

One PostgreSQL cluster, one database, six schemas. DDL below is design-level: names and
shapes are contractual; exact index tuning happens in implementation.

Conventions: `bigint generated always as identity` PKs for high-volume tables, `uuid`
external IDs where values cross system boundaries, `timestamptz` everywhere, `text` +
CHECK over enums where states will evolve, native `cidr`/`inet` types for routing.

## Schema `routing`

```sql
create table routing.snapshot (
  id               bigint primary key generated always as identity,
  version          text not null unique,            -- e.g. 20260718T0800Z-1723118400000
  source_uri       text not null,                   -- RIS bview identity
  source_timestamp timestamptz not null,
  status           text not null default 'building'
                   check (status in ('building','published','superseded','failed')),
  asn_count        int, prefix_count_v4 int, prefix_count_v6 int,
  artifact_sha256  text, artifact_size_bytes bigint,
  artifact_signature text,                          -- Ed25519 over manifest
  built_at         timestamptz, published_at timestamptz,
  created_at       timestamptz not null default now()
);

create table routing.provider (                     -- cache of VPS Advisor data
  provider_id      uuid primary key,                -- VPS Advisor's ID, verbatim
  name             text not null,
  monitoring_enabled boolean not null,
  priority         int not null default 100,
  synced_at        timestamptz not null,
  delisted_at      timestamptz                      -- soft-delete on disappearance
);

create table routing.asn (
  asn              bigint primary key,
  provider_id      uuid not null references routing.provider,
  registry_name    text, registry_country text,
  synced_at        timestamptz not null
);

create table routing.prefix (
  id               bigint primary key generated always as identity,
  snapshot_id      bigint not null references routing.snapshot,
  prefix           cidr not null,
  origin_asn       bigint not null,                 -- FK to routing.asn
  geo_country      text, geo_city text,
  geo_lat double precision, geo_lon double precision,
  flags            jsonb not null default '{}',     -- {bogon, moas_conflict, ...}
  unique (snapshot_id, prefix, origin_asn)
);
create index on routing.prefix using gist (prefix inet_ops);

create table routing.probe_target (
  id               bigint primary key generated always as identity,
  snapshot_id      bigint not null references routing.snapshot,
  provider_id      uuid not null,
  prefix_id        bigint not null references routing.prefix,
  address          inet not null,
  rationale        text not null,                   -- how it was selected
  active           boolean not null default true,
  unique (snapshot_id, address)
);
```

Notes: prefixes are per-snapshot (immutable builds), not mutated in place — diffing two
snapshots yields routing-churn signals for the anomaly detector. Old snapshots are pruned
after a retention window, keeping summary rows.

## Schema `registry`

```sql
create table registry.worker (
  id               uuid primary key,
  operator_id      uuid not null,                   -- VPS Advisor account ID
  name             text not null,
  state            text not null default 'pending'
                   check (state in ('pending','active','suspended','quarantined','retired')),
  state_reason     text,
  software_version text,
  reported_country text, verified_country text,     -- GeoIP of source IP
  source_asn       bigint,                          -- network the worker probes FROM
  enrolled_at      timestamptz not null default now(),
  approved_at      timestamptz, retired_at timestamptz,
  last_heartbeat_at timestamptz,
  config           jsonb not null default '{}'      -- pushed config overrides
);

create table registry.worker_key (
  id               bigint primary key generated always as identity,
  worker_id        uuid not null references registry.worker,
  public_key       bytea not null,                  -- Ed25519, 32 bytes
  valid_from       timestamptz not null default now(),
  valid_until      timestamptz,                     -- null = current
  revoked_at       timestamptz, revoke_reason text
);
create unique index one_current_key on registry.worker_key (worker_id)
  where valid_until is null and revoked_at is null;

create table registry.enrollment_token (
  token_hash       bytea primary key,               -- sha256; plaintext never stored
  worker_id        uuid not null references registry.worker,
  expires_at       timestamptz not null,
  used_at          timestamptz
);

create table registry.trust_score (
  worker_id        uuid primary key references registry.worker,
  score            double precision not null check (score between 0 and 1),
  components       jsonb not null,   -- {agreement, availability, tenure, penalties}
  computed_at      timestamptz not null
);

create table registry.trust_event (                  -- append-only
  id               bigint primary key generated always as identity,
  worker_id        uuid not null references registry.worker,
  event_type       text not null,    -- approved, suspended, disagreement, bad_signature, ...
  detail           jsonb not null default '{}',
  actor            text not null,    -- 'system' | admin identifier
  created_at       timestamptz not null default now()
);
```

## Schema `scheduling`

```sql
create table scheduling.assignment (
  id               bigint primary key generated always as identity,
  target_id        bigint not null,                 -- routing.probe_target
  provider_id      uuid not null,
  probe_type       text not null,                   -- 'icmp' (v1), extensible
  interval_seconds int not null,
  params           jsonb not null default '{}',
  redundancy_group uuid not null,                   -- workers covering same target
  status           text not null default 'open'
                   check (status in ('open','leased','draining','closed')),
  created_at       timestamptz not null default now(),
  closed_at        timestamptz
);

create table scheduling.lease (
  id               bigint primary key generated always as identity,
  assignment_id    bigint not null references scheduling.assignment,
  worker_id        uuid not null,
  leased_at        timestamptz not null default now(),
  expires_at       timestamptz not null,            -- renewed by heartbeat
  released_at      timestamptz, release_reason text
);
create unique index one_live_lease on scheduling.lease (assignment_id, worker_id)
  where released_at is null;
```

## Schema `measurements` (write hotspot; partitioned)

```sql
create table measurements.observation (
  id               bigint generated always as identity,
  worker_id        uuid not null,
  assignment_id    bigint not null,
  provider_id      uuid not null,
  target           inet not null,
  probe_type       text not null,
  measured_at      timestamptz not null,            -- worker clock
  received_at      timestamptz not null default now(),
  ok               boolean not null,
  rtt_ms           double precision,
  packets_sent     int, packets_lost int,
  metrics          jsonb not null default '{}',     -- protocol-specific extras
  signature        bytea not null,
  primary key (id, measured_at)
) partition by range (measured_at);                  -- daily partitions
create index on measurements.observation (provider_id, measured_at);
create index on measurements.observation (worker_id, measured_at);
```

Retention: raw partitions dropped after N days (default 14); aggregates carry history.
Uploads are batched; the coordinator inserts via COPY.

## Schema `aggregation`

```sql
create table aggregation.consensus_window (
  id               bigint primary key generated always as identity,
  provider_id      uuid not null,
  region           text not null,                   -- worker-region bucket; 'global'
  probe_type       text not null,
  window_start     timestamptz not null,
  window_seconds   int not null,
  verdict          text not null
                   check (verdict in ('healthy','degraded','outage','insufficient_data')),
  confidence       double precision not null,
  worker_count     int not null, dissent_ratio double precision not null,
  loss_rate        double precision,
  rtt_p50 double precision, rtt_p95 double precision, rtt_p99 double precision,
  detail           jsonb not null default '{}',
  unique (provider_id, region, probe_type, window_start, window_seconds)
);

create table aggregation.provider_status (           -- current state, one row per scope
  provider_id      uuid not null,
  region           text not null,
  verdict          text not null,
  confidence       double precision not null,
  since            timestamptz not null,
  metrics          jsonb not null,
  updated_at       timestamptz not null,
  primary key (provider_id, region)
);

create table aggregation.anomaly (
  id               bigint primary key generated always as identity,
  provider_id      uuid not null,
  kind             text not null,   -- reachability_loss, latency_regression, routing_churn
  region           text, severity text not null,
  opened_at        timestamptz not null, resolved_at timestamptz,
  evidence         jsonb not null default '{}'
);

create table aggregation.publication_outbox (
  id               bigint primary key generated always as identity,
  kind             text not null,                   -- provider_status, anomaly, telemetry
  payload          jsonb not null,
  created_at       timestamptz not null default now(),
  attempts         int not null default 0,
  next_attempt_at  timestamptz not null default now(),
  acked_at         timestamptz
);
```

## Schema `audit`

```sql
create table audit.event (                           -- append-only, no updates/deletes
  id               bigint primary key generated always as identity,
  category         text not null,   -- auth, admin, snapshot, security
  actor            text not null,
  action           text not null,
  subject          text,
  detail           jsonb not null default '{}',
  created_at       timestamptz not null default now()
);
```

Also in `registry`: a `replay_nonce` table (worker_id, nonce, seen_at) with short TTL
pruning, backing replay protection until/unless a Redis cache is introduced.

## Cross-cutting

- **Migrations:** versioned SQL migrations (golang-migrate or equivalent), one migration
  stream for the whole database, applied by an init job.
- **Roles:** one Postgres role per service with least privilege (builder cannot touch
  `measurements`; coordinator cannot write `aggregation`; aggregator reads
  `measurements`, writes `aggregation` + `registry.trust_*`).
- **Backups:** nightly compressed `pg_dump` of the whole database, readback-verified
  and optionally copied offsite; measurements partitions are re-derivable pain but
  aggregates and registry are precious. See
  [backup & restore](../operations/backup-restore.md).
