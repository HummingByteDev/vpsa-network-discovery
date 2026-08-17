-- Geographic network intelligence.
--
-- The platform already enriched every announced prefix with a country
-- (routing.prefix.geo_*), but nothing downstream ever *aggregated* that: the
-- artifact dropped it, probe targets were picked without regard to it, and the
-- consensus engine only ever wrote region='global'. VPS Advisor therefore
-- received a single global verdict for a provider whose network may span a
-- dozen countries.
--
-- Three additions close that gap:
--   1. routing.provider_geo   — per-snapshot country distribution of a
--                               provider's announced IPv4/IPv6 address space.
--   2. probe_target.geo_*     — the country/city a target represents, so
--                               measurements can be attributed without a join
--                               chain that outlives the snapshot.
--   3. aggregation.target_status — current per-target (= per-network) health,
--                               the row behind the "monitored networks" view.

-- 1. Provider network distribution -----------------------------------------
--
-- One row per (snapshot, provider, country). Address counts are of *exclusive*
-- address space: a more-specific announcement never counts its addresses twice
-- against the covering announcement (see internal/builder/distribution.go).
--
-- country_code is ISO 3166-1 alpha-2, or the reserved code 'ZZ' for address
-- space the GeoIP database does not attribute to any country. 'ZZ' is never
-- rendered as a real country.
--
-- ipv6_net64s counts /64 networks, not addresses: IPv6 address counts do not
-- fit in any integer type and are meaningless as a share.
create table routing.provider_geo (
  snapshot_id     bigint not null references routing.snapshot,
  provider_id     text   not null,
  country_code    text   not null,
  country_name    text,
  continent_code  text,
  continent_name  text,
  ipv4_addresses  bigint not null default 0,
  ipv6_net64s     bigint not null default 0,
  ipv4_share      double precision not null default 0, -- percent of provider IPv4 space
  prefix_count_v4 int    not null default 0,
  prefix_count_v6 int    not null default 0,
  target_count    int    not null default 0,
  primary key (snapshot_id, provider_id, country_code)
);
create index provider_geo_provider_idx on routing.provider_geo (provider_id);

-- 2. Country/city on the probe target ---------------------------------------
-- Denormalized from the target's prefix at derivation time. Keeps the
-- aggregation join to one table and one index lookup per observation batch.
alter table routing.probe_target add column geo_country text;
alter table routing.probe_target add column geo_city    text;
create index probe_target_address_idx on routing.probe_target (address);

-- 3. Current per-target health ----------------------------------------------
-- Refreshed on every status rollup from the trailing measurement window. Not
-- history: bounded by the number of live targets, so it stays small. The
-- consensus history lives in aggregation.consensus_window.
create table aggregation.target_status (
  provider_id      text not null,
  target           inet not null,
  region           text not null default 'ZZ',
  prefix           cidr,
  origin_asn       bigint,
  city             text,
  verdict          text not null
                   check (verdict in ('healthy','degraded','outage','insufficient_data')),
  availability     double precision,
  loss_rate        double precision,
  rtt_p50          double precision,
  rtt_p95          double precision,
  worker_count     int not null default 0,
  last_measured_at timestamptz,
  updated_at       timestamptz not null default now(),
  primary key (provider_id, target)
);
create index target_status_provider_idx on aggregation.target_status (provider_id, region);
