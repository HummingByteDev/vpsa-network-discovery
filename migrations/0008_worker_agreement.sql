-- Per-window consensus agreement per worker: the trust scorer's dominant
-- component (docs/architecture/05 §4). Written by the aggregator when a
-- window settles; scored against the settled result, so a worker that is
-- right early in an outage is not punished by the instantaneous majority.
create table aggregation.worker_agreement (
  worker_id    uuid not null,
  window_start timestamptz not null,
  agreement    double precision not null check (agreement between 0 and 1),
  targets      int not null,
  primary key (worker_id, window_start)
);
create index worker_agreement_recent on aggregation.worker_agreement (worker_id, window_start desc);
