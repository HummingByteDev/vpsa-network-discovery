-- Worker registry: identity, keys, enrollment, trust, replay-nonce cache.
create schema if not exists registry;

create table registry.worker (
  id                uuid primary key,
  operator_id       uuid not null,
  name              text not null,
  state             text not null default 'pending'
                    check (state in ('pending','active','suspended','quarantined','retired')),
  state_reason      text,
  software_version  text,
  reported_country  text,
  verified_country  text,
  source_asn        bigint,
  enrolled_at       timestamptz not null default now(),
  approved_at       timestamptz,
  retired_at        timestamptz,
  last_heartbeat_at timestamptz,
  config            jsonb not null default '{}'
);

create table registry.worker_key (
  id          bigint primary key generated always as identity,
  worker_id   uuid not null references registry.worker,
  public_key  bytea not null,
  valid_from  timestamptz not null default now(),
  valid_until timestamptz,
  revoked_at  timestamptz,
  revoke_reason text
);
create unique index one_current_key on registry.worker_key (worker_id)
  where valid_until is null and revoked_at is null;

create table registry.enrollment_token (
  token_hash bytea primary key,
  worker_id  uuid not null references registry.worker,
  expires_at timestamptz not null,
  used_at    timestamptz
);

create table registry.trust_score (
  worker_id   uuid primary key references registry.worker,
  score       double precision not null check (score between 0 and 1),
  components  jsonb not null,
  computed_at timestamptz not null
);

create table registry.trust_event (
  id         bigint primary key generated always as identity,
  worker_id  uuid not null references registry.worker,
  event_type text not null,
  detail     jsonb not null default '{}',
  actor      text not null,
  created_at timestamptz not null default now()
);

-- Replay protection: nonce uniqueness inside the signature timestamp window.
-- TTL-pruned by the coordinator; may move to a cache tier at larger scale.
create table registry.replay_nonce (
  worker_id uuid not null references registry.worker,
  nonce     bytea not null,
  seen_at   timestamptz not null default now(),
  primary key (worker_id, nonce)
);
create index replay_nonce_seen_idx on registry.replay_nonce (seen_at);
