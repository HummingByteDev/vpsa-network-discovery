-- Routing intelligence: snapshots, provider/ASN cache, prefixes, probe targets.
create schema if not exists routing;

create table routing.snapshot (
  id                 bigint primary key generated always as identity,
  version            text not null unique,
  source_uri         text not null,
  source_timestamp   timestamptz not null,
  status             text not null default 'building'
                     check (status in ('building','published','superseded','failed')),
  asn_count          int,
  prefix_count_v4    int,
  prefix_count_v6    int,
  artifact_sha256    text,
  artifact_size_bytes bigint,
  artifact_signature text,
  built_at           timestamptz,
  published_at       timestamptz,
  created_at         timestamptz not null default now()
);

-- Cache of VPS Advisor provider data (VPS Advisor is the source of truth;
-- rows are provenance-tagged and soft-deleted when delisted upstream).
create table routing.provider (
  provider_id        uuid primary key,
  name               text not null,
  monitoring_enabled boolean not null,
  priority           int not null default 100,
  synced_at          timestamptz not null,
  delisted_at        timestamptz
);

create table routing.asn (
  asn              bigint primary key,
  provider_id      uuid not null references routing.provider,
  registry_name    text,
  registry_country text,
  synced_at        timestamptz not null
);

create table routing.prefix (
  id          bigint primary key generated always as identity,
  snapshot_id bigint not null references routing.snapshot,
  prefix      cidr not null,
  origin_asn  bigint not null references routing.asn,
  geo_country text,
  geo_city    text,
  geo_lat     double precision,
  geo_lon     double precision,
  flags       jsonb not null default '{}',
  unique (snapshot_id, prefix, origin_asn)
);
create index prefix_inet_idx on routing.prefix using gist (prefix inet_ops);

create table routing.probe_target (
  id          bigint primary key generated always as identity,
  snapshot_id bigint not null references routing.snapshot,
  provider_id uuid not null references routing.provider,
  prefix_id   bigint not null references routing.prefix,
  address     inet not null,
  rationale   text not null,
  active      boolean not null default true,
  unique (snapshot_id, address)
);
