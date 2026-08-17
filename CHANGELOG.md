# Changelog

All notable changes to the VAPN platform. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the project uses
[semantic versioning](docs/operations/releases-and-upgrades.md#versioning),
one version for the whole platform.

## [Unreleased]

### Geographic network intelligence

The platform enriched every announced prefix with a country but never
aggregated it: the published status document carried a single global verdict.
For a provider like AS200019 (AlexHost SRL), whose IPv4 space spans ten
countries, that discarded the most useful thing the BGP + GeoIP pipeline knows.
This release carries geography end to end — from announcement to published
document.

#### Added

- **Per-provider country distribution** (`routing.provider_geo`). Each build
  records, per country: IPv4 address count, share of the provider's total, IPv6
  `/64` count, contributing prefix counts and probe-target count.
  - Counts **exclusive** address space: a more-specific announcement never
    counts its addresses again against the covering one (`/16` + `/17` + `/24`
    is 65 536 addresses, not 98 560).
  - Shares are **address-weighted**, computed after deduplication — a `/20` is
    sixteen `/24`s — never derived from prefix counts.
  - Announcements are split at the **GeoIP database's own record boundaries**,
    so a prefix spanning two countries is attributed to both in the right
    proportion instead of being labelled from its first address.
  - Address space MaxMind does not place is reported under the reserved code
    `ZZ`, never folded into a real country and never dropped from the total.
  - No prefix is ever expanded into individual addresses; the cost of a `/8` is
    the number of GeoIP records inside it.
- **Country-level monitoring.** Every settled consensus window now produces one
  global rollup *and* one per country, from the same trust-weighted votes.
  `aggregation.consensus_window.region` / `provider_status.region` hold `global`
  or an ISO 3166-1 alpha-2 code.
- **Per-network health** (`aggregation.target_status`): current verdict,
  availability over a trailing window, latency percentiles, loss, contributing
  workers and last measurement time for each monitored network.
- **New status-document sections** (contract A4): `regions[]` (per-country
  verdicts with coverage counts), `network` (the provider's IPv4/IPv6
  distribution by country), `networks[]` (per-monitored-network health with
  prefix, ASN, country, city).
- **Country/city on probe targets and in the snapshot artifact.** The artifact's
  `prefixes` table gains `geo_city`; `targets` gains `geo_country` and
  `geo_city`.
- **`VAPN_MAX_TARGETS_PER_COUNTRY`** (default `10`) — per-country probe-target
  cap within the per-provider budget.
- **`VAPN_TARGET_HEALTH_WINDOW`** (default `24h`) — trailing window for
  per-network availability.
- **`VAPN_CADDY_HTTP_PORT` / `VAPN_CADDY_HTTPS_PORT`** (defaults `80` / `443`) —
  configurable edge ports.
- **`vapn reconfigure`** — re-run the worker's configuration questions with
  current values as defaults.
- Migration `0010_geographic.sql`.

#### Changed

- **Probe-target derivation is country-aware.** The per-provider budget is
  filled country by country (each country's best target, then each country's
  second, …) instead of purely by prefix size. A provider whose largest
  announcements are all in one country is now measurable in the smaller ones —
  previously they could never receive a target, so a country-level verdict was
  impossible in principle.
- **Consensus aggregation groups by region as well as globally.** The global
  rollup is computed from exactly the same votes as before and is unchanged.
- **Status rollup publishes one document per provider** containing global,
  regional, network-distribution and per-network sections. `provider_id`,
  `as_of` and `global` are byte-for-byte what earlier releases sent.
- **`internal/routing/geo`** reads GeoLite2-City through `maxminddb` directly
  (rather than `geoip2`) so prefix lookups and record-boundary traversal share
  one decoder; `geo.Info` gains country name, continent code and continent name.
- **The edge's ports are set from configuration**, with host and container port
  kept identical and the same values passed to Caddy as `http_port` /
  `https_port`, so Caddy's redirects and certificate challenges match the ports
  the outside world sees.
- **`vapn install` and `vapn reconfigure` share one prompt flow**, so both offer
  the same settings, defaults and secret handling.
- **`~/.vapn/config.env` is rewritten in a fixed order** on every save, and
  settings added by hand are now preserved rather than dropped.

#### Fixed

- **Missing geographic information** in the published artifact and status
  document — the root cause of provider records that contained only a global
  verdict.
- **Insufficient provider monitoring response**: a country's health is no longer
  averaged into a single global number, and coverage counts distinguish "not
  measured here" from "down here".
- **Caddy port collision.** `docker compose up -d` failed with
  `failed to bind host port 0.0.0.0:80/tcp: address already in use` on any VM
  already serving port 80, with no supported way to change it.
- **Snapshot pruning failed on any platform that had scheduled work.** Closed
  assignments and their released leases reference a snapshot's probe targets, so
  `Publisher.Prune` hit `assignment_target_id_fkey` and every build after
  `VAPN_RETAIN_SNAPSHOTS` was exceeded ended in `prune: ...`. Pruning now
  removes the scheduling history belonging to a pruned snapshot first. Found
  while extending the pipeline; regression test:
  `TestPruneWithScheduledWork`.

#### Compatibility

Verified by the test suite unless stated otherwise.

| Surface | Compatibility |
|---|---|
| **Worker protocol** | **Unchanged.** No coordinator endpoint, request or response used by workers changed. |
| **Snapshot artifact** | **Backward compatible.** Columns were only added (`prefixes.geo_city`, `targets.geo_country`, `targets.geo_city`); existing columns and the `targets` lookup workers perform are untouched. `min_worker_version` is **not** raised. |
| **Results API (A4)** | **Backward compatible, additive.** `provider_id`, `as_of` and `global` are unchanged; `regions`, `network` and `networks` are new keys. A consumer reading only `global` is unaffected — provided it stores/ignores unknown fields, as the integration guide has always required. |
| **Provider catalog / enrollment / decisions (A1–A3)** | Unchanged. |
| **Admin API and `vapnctl`** | Unchanged. |
| **Database** | Migration `0010_geographic.sql` is **expand-only**: one new table per schema plus two nullable columns. The previous release runs against it unchanged, so the standard rolling upgrade applies. |
| **Configuration** | **No migration required.** Every new variable has a default that preserves current behaviour: edge ports stay `80`/`443`, and the per-country target cap only redistributes an existing budget. |

**Upgrade:** the standard procedure in
[releases and upgrades](docs/operations/releases-and-upgrades.md#upgrade) —
`docker compose pull && docker compose up -d` runs the migration first.

**Two things only appear after the next build**, because they are produced by
the builder, not backfilled: the country distribution (`routing.provider_geo`)
and country tags on probe targets. Until then, regional sections are absent and
every measurement aggregates under region `ZZ`. Run one build to populate them:

```sh
cd /opt/vapn/deploy/prod && docker compose run --rm builder
```

**A GeoIP database is required for any of this to be meaningful.** Without
`VAPN_GEOIP_CITY_MMDB` the builder warns and every country resolves to `ZZ` —
correct, but not useful. See
[Group 5 — Location data](docs/builder/installation.md#step-4--configure-the-builder).
