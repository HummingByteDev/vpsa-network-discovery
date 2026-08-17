# 06 — Lifecycles

## 1. Worker lifecycle

```
            operator creates worker on VPS Advisor (gets one-time token)
                                   │
                                   ▼
 worker boots ──► generates keypair ──► POST /register (token + pubkey)
                                   │
                                   ▼
                              ┌─────────┐   admin approves (site)
                              │ pending │ ─────────────────────────┐
                              └─────────┘                          ▼
        admin suspends ┌──────────────────────────────────── ┌─────────┐
       ┌───────────────┤                                     │ active  │
       ▼               │        trust collapse /             └────┬────┘
 ┌───────────┐         │        anomaly (system) ──► ┌─────────────┐│
 │ suspended │◄────────┘                             │ quarantined ││ (shadow
 └────┬──────┘   admin reinstates ◄──────────────────┴─────────────┘│  mode)
      │                                                             │
      │        admin retires (any state)                            │
      └───────────────►  ┌─────────┐  ◄─────────────────────────────┘
                         │ retired │   keys revoked, history kept, terminal
                         └─────────┘
```

States and behavior:

| State | Auth | Assignments | Consensus weight |
|---|---|---|---|
| pending | register/heartbeat only | none | 0 |
| active | full | yes | trust-based |
| suspended | rejected (403) | none (leases revoked) | 0 |
| quarantined | full | yes (shadow) | 0 — observations recorded for trust rebuild |
| retired | rejected, keys revoked | none | 0, forever |

Steady-state loop (active worker): heartbeat (~30s) → receives config + control actions
+ artifact version advertisements → downloads/verifies new artifacts when advertised
(atomic swap: download to temp, verify hash+signature, rename) → leases assignments →
probes on interval → uploads signed batches (~60s or size threshold) → renews leases via
heartbeat. Missed heartbeats: leases expire → scheduler reassigns → prolonged silence
flags the worker `unreachable` (an attribute, not a state) for the admin dashboard.

Upgrades: worker image is versioned; heartbeat reports version; coordinator may respond
`upgrade_required` (below min version → drained + refused leases until upgraded).
Community operators upgrade with `vapn update` — health-gated with automatic rollback —
or unattended via the shipped `vapn-update.timer` (daily, randomized). See
[operating a worker](../worker/operations.md#updating).

Shutdown: SIGTERM → release leases (`/assignments/release`) → flush upload queue → exit.

## 2. Snapshot lifecycle

```
 (cron) builder run
   1. sync monitored ASN list  ── VPS Advisor Provider API
   2. fetch RIS bview          ── (dev: data/ripe/latest-bview.gz)
   3. parse MRT → candidate prefixes for monitored ASNs only
   4. validate: dedupe, bogon filter, MOAS conflict flagging
   5. enrich: GeoIP (country/city/coords) from the builder's local GeoLite2 copy
      (fetched from MaxMind with the platform operator's own licence key)
   6. load into routing.* under new snapshot version   [status: building]
   6b. country distribution: exclusive IPv4 space per provider per country
       → routing.provider_geo (address-weighted shares; unplaced space = ZZ)
   7. derive probe targets (representative address per prefix, country-tagged,
      budget filled country by country so the whole footprint is measurable)
   8. sanity gate: |Δ prefix_count| vs previous > threshold? → hold for
      admin approval; else auto-continue
   9. export SQLite artifact + manifest (sha256, Ed25519 signature)
  10. upload to artifact store; verify readback
  11. mark published; previous snapshot → superseded      [status: published]
  12. coordinator advertises new version in heartbeats
  13. scheduler drains assignments on removed targets, issues new ones
  14. workers download, verify, atomically swap
  15. retention: prune superseded snapshots’ prefix rows, country distribution
      and the scheduling history that referenced their targets, after N versions
```

Failure handling: any step failing aborts the build, leaving the new snapshot row in
`building` (the sanity gate does the same, with exit code 2) and the previous
`published` version fully in force — publication is atomic from the workers' view.
Note the implementation never writes the `failed` status the schema allows; an
abandoned build is simply one that never left `building`.
Rollback = re-pointing `current` at a prior version (admin action, audited).

Cadence: routing snapshots are built three times a day, at 00:30/08:30/16:30 UTC —
half an hour behind RIS's own 8-hourly bview publication (routing *churn* signals come
from snapshot diffs). The builder's
GeoLite2 refresh is an independent job on a 72-hour cycle (direct MaxMind download,
operator's own key — never redistributed; see risk R8) — a GeoIP update never requires a routing
rebuild and vice versa.

SQLite artifact contents (worker-facing subset only): `prefixes(prefix, origin_asn,
provider_id, geo_country, geo_city)`, `targets(address, provider_id, prefix,
geo_country, geo_city)`, and a `meta` key/value table carrying `version` and
`min_worker_version`. Columns are only ever added, never removed or reordered —
workers select what they know by name, so an older worker reads a newer
artifact unchanged and `min_worker_version` need not move. Workers use it for local
validation ("is this target still legitimate?") and enrichment — never for choosing
targets.

## 3. Provider sync lifecycle

Every few minutes the coordinator pulls provider/ASN deltas (`updated_since` cursor).
- New provider or ASN → included in next snapshot build; until then, no targets exist
  (monitoring begins after first snapshot containing it).
- `monitoring_enabled=false` or delisted → immediate: scheduler drains assignments,
  aggregator stops publishing, rows soft-deleted (`delisted_at`); next snapshot drops
  the prefixes.
- ASN moving between providers → treated as delist + add; flagged in audit for review.
