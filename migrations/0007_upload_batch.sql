-- Idempotent observation uploads: a batch ID is accepted exactly once.
create table measurements.upload_batch (
  batch_id          uuid primary key,
  worker_id         uuid not null,
  received_at       timestamptz not null default now(),
  observation_count int not null
);
