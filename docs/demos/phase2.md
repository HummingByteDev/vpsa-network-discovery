# Phase 2 Demo — Routing Snapshot Builder

What exists: the full snapshot pipeline — provider/ASN sync from the (mock) VPS
Advisor API, streaming MRT TABLE_DUMP_V2 parsing of a RIPE RIS bview, prefix
extraction limited to monitored ASNs, bogon dropping, MOAS + long-prefix
flagging, GeoLite2 City enrichment, versioned PostgreSQL load, probe-target
derivation, sanity gate, and atomic publish/supersede.

## Prerequisites

- Phase 1 dev stack running (`make dev-up`): postgres on host 5433, mockadvisor on 8081.
- `data/ripe/latest-bview.gz` (pre-downloaded RIS dump).
- Extracted GeoLite2 City database: `data/geo-data/GeoLite2-City.mmdb`
  (from the pre-downloaded tarball; in production the builder deployer fetches
  it from MaxMind with their own license key — never redistributed).

## 1. Run a build

```sh
make build
VAPN_DB_DSN=postgres://vapn:vapn-dev@localhost:5433/vapn \
VAPN_ADVISOR_URL=http://localhost:8081 \
VAPN_ADVISOR_TOKEN=dev-advisor-token \
VAPN_GEOIP_CITY_MMDB=data/geo-data/GeoLite2-City.mmdb \
./bin/builder
```

The log shows each stage: provider sync (4 enabled fixture providers → 4 ASNs:
Hetzner 24940, OVH 16276, DigitalOcean 14061, Contabo 51167), extraction over
~1.4 M RIB records (~85 s), enrichment, load, target derivation, gate, publish.
Reference result on the bundled June 2026 bview: 2,267 prefixes (2,194 v4 /
73 v6), 2,266 geolocated, 5 MOAS-flagged, 462 probe targets.

Useful knobs: `VAPN_MAX_TARGETS_PER_PROVIDER` (default 100 per address family),
`VAPN_SANITY_MAX_DELTA_PCT` (default 50), `VAPN_SANITY_FORCE=true` to override
a tripped gate (exit code 2 = snapshot held in `building` for review).

## 2. Inspect the result

```sh
psql postgres://vapn:vapn-dev@localhost:5433/vapn <<'SQL'
select version, status, asn_count, prefix_count_v4, prefix_count_v6
  from routing.snapshot order by id desc limit 3;

select a.provider_id, p.name, count(*) prefixes
  from routing.prefix x
  join routing.asn a on a.asn = x.origin_asn
  join routing.provider p on p.provider_id = a.provider_id
  join routing.snapshot s on s.id = x.snapshot_id and s.status = 'published'
  group by 1, 2 order by 3 desc;

-- MOAS conflicts and geo coverage
select count(*) filter (where (flags->>'moas')::bool) as moas,
       count(*) filter (where geo_country is not null) as geolocated,
       count(*) as total
  from routing.prefix x join routing.snapshot s on s.id = x.snapshot_id
  where s.status = 'published';

select p.name, count(*) targets
  from routing.probe_target t
  join routing.provider p using (provider_id)
  join routing.snapshot s on s.id = t.snapshot_id and s.status = 'published'
  group by 1 order by 2 desc;
SQL
```

## 3. Run it again

A second run creates a new snapshot version, passes the sanity gate (counts
match), publishes it, and marks the previous one `superseded` — workers-facing
artifact export of the published snapshot is Phase 3.

## 4. Tests

```sh
make test                                  # golden-file MRT tests, bogon table tests
make test   # DB tests run against vapn_test (make test-db once)   # + end-to-end pipeline
```

The golden-file tests build a synthetic bview (via gobgp serialization) with
known MOAS / AS_SET / bogon / IPv6 / unmonitored cases and assert extraction
byte-for-byte; the integration test runs the whole pipeline twice against a
real PostgreSQL and checks published/superseded states, flags, and targets.
**Note:** the integration test truncates the `routing` schema of the database
it points at.
