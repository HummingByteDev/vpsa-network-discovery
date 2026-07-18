-- Aggregation: consensus windows, current status, anomalies, publication outbox.
create schema if not exists aggregation;

create table aggregation.consensus_window (
  id             bigint primary key generated always as identity,
  provider_id    uuid not null,
  region         text not null,
  probe_type     text not null,
  window_start   timestamptz not null,
  window_seconds int not null check (window_seconds > 0),
  verdict        text not null
                 check (verdict in ('healthy','degraded','outage','insufficient_data')),
  confidence     double precision not null check (confidence between 0 and 1),
  worker_count   int not null,
  dissent_ratio  double precision not null,
  loss_rate      double precision,
  rtt_p50        double precision,
  rtt_p95        double precision,
  rtt_p99        double precision,
  detail         jsonb not null default '{}',
  unique (provider_id, region, probe_type, window_start, window_seconds)
);

create table aggregation.provider_status (
  provider_id uuid not null,
  region      text not null,
  verdict     text not null,
  confidence  double precision not null,
  since       timestamptz not null,
  metrics     jsonb not null,
  updated_at  timestamptz not null,
  primary key (provider_id, region)
);

create table aggregation.anomaly (
  id          bigint primary key generated always as identity,
  provider_id uuid not null,
  kind        text not null,
  region      text,
  severity    text not null,
  opened_at   timestamptz not null,
  resolved_at timestamptz,
  evidence    jsonb not null default '{}'
);
create index anomaly_open_idx on aggregation.anomaly (provider_id) where resolved_at is null;

create table aggregation.publication_outbox (
  id              bigint primary key generated always as identity,
  kind            text not null,
  payload         jsonb not null,
  created_at      timestamptz not null default now(),
  attempts        int not null default 0,
  next_attempt_at timestamptz not null default now(),
  acked_at        timestamptz
);
create index outbox_pending_idx on aggregation.publication_outbox (next_attempt_at)
  where acked_at is null;
