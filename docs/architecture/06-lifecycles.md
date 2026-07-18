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
Community operators upgrade via `docker pull` / compose; watchtower-style auto-pull is
documented but optional.

Shutdown: SIGTERM → release leases (`/assignments/release`) → flush upload queue → exit.

## 2. Snapshot lifecycle

```
 (cron) builder run
   1. sync monitored ASN list  ── VPS Advisor Provider API
   2. fetch RIS bview          ── (dev: data/ripe/latest-bview.gz)
   3. parse MRT → candidate prefixes for monitored ASNs only
   4. validate: dedupe, bogon filter, MOAS conflict flagging
   5. enrich: GeoIP (country/city/coords) from the builder's local GeoLite2 copy
      (fetched from MaxMind with the deployer's own license key)
   6. load into routing.* under new snapshot version   [status: building]
   7. derive probe targets (representative addresses per prefix)
   8. sanity gate: |Δ prefix_count| vs previous > threshold? → hold for
      admin approval; else auto-continue
   9. export SQLite artifact + manifest (sha256, Ed25519 signature)
  10. upload to artifact store; verify readback
  11. mark published; previous snapshot → superseded      [status: published]
  12. coordinator advertises new version in heartbeats
  13. scheduler drains assignments on removed targets, issues new ones
  14. workers download, verify, atomically swap
  15. retention: prune superseded snapshots’ prefix rows after N versions
```

Failure handling: any step failing marks the snapshot `failed` and leaves the previous
`published` version fully in force — publication is atomic from the workers' view.
Rollback = re-pointing `current` at a prior version (admin action, audited).

Cadence: routing snapshots daily (RIS bview cadence: 8h dumps; daily is enough for
prefix membership, and routing *churn* signals come from snapshot diffs). The builder's
GeoLite2 refresh is an independent weekly job (direct MaxMind download, deployer's own
key — never redistributed; see risk R8) — a GeoIP update never requires a routing
rebuild and vice versa.

SQLite artifact contents (worker-facing subset only): `prefixes(prefix, origin_asn,
provider_id)`, `targets(address, provider_id, assignment hints)`, `meta(version,
built_at, min_worker_version)`. Workers use it for local validation ("is this target
still legitimate?") and enrichment — never for choosing targets.

## 3. Provider sync lifecycle

Every few minutes the coordinator pulls provider/ASN deltas (`updated_since` cursor).
- New provider or ASN → included in next snapshot build; until then, no targets exist
  (monitoring begins after first snapshot containing it).
- `monitoring_enabled=false` or delisted → immediate: scheduler drains assignments,
  aggregator stops publishing, rows soft-deleted (`delisted_at`); next snapshot drops
  the prefixes.
- ASN moving between providers → treated as delist + add; flagged in audit for review.
