-- Scheduling: assignments and leases.
create schema if not exists scheduling;

create table scheduling.assignment (
  id               bigint primary key generated always as identity,
  target_id        bigint not null references routing.probe_target,
  provider_id      uuid not null references routing.provider,
  probe_type       text not null,
  interval_seconds int not null check (interval_seconds > 0),
  params           jsonb not null default '{}',
  redundancy_group uuid not null,
  status           text not null default 'open'
                   check (status in ('open','leased','draining','closed')),
  created_at       timestamptz not null default now(),
  closed_at        timestamptz
);
create index assignment_group_idx on scheduling.assignment (redundancy_group);
create index assignment_status_idx on scheduling.assignment (status);

create table scheduling.lease (
  id             bigint primary key generated always as identity,
  assignment_id  bigint not null references scheduling.assignment,
  worker_id      uuid not null references registry.worker,
  leased_at      timestamptz not null default now(),
  expires_at     timestamptz not null,
  released_at    timestamptz,
  release_reason text
);
create unique index one_live_lease on scheduling.lease (assignment_id, worker_id)
  where released_at is null;
create index lease_expiry_idx on scheduling.lease (expires_at) where released_at is null;
