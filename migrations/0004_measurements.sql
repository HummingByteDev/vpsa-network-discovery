-- Measurements: raw signed observations, range-partitioned by measured_at.
-- Daily partition management (create-ahead, retention drops) arrives with the
-- ingestion path in Phase 5; the DEFAULT partition keeps dev/test inserts
-- working until then.
create schema if not exists measurements;

create table measurements.observation (
  id            bigint generated always as identity,
  worker_id     uuid not null,
  assignment_id bigint not null,
  provider_id   uuid not null,
  target        inet not null,
  probe_type    text not null,
  measured_at   timestamptz not null,
  received_at   timestamptz not null default now(),
  ok            boolean not null,
  rtt_ms        double precision,
  packets_sent  int,
  packets_lost  int,
  metrics       jsonb not null default '{}',
  signature     bytea not null,
  primary key (id, measured_at)
) partition by range (measured_at);

create index observation_provider_idx on measurements.observation (provider_id, measured_at);
create index observation_worker_idx on measurements.observation (worker_id, measured_at);

create table measurements.observation_default
  partition of measurements.observation default;
