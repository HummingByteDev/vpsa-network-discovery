# 02 — Domain Model

Entities, their ownership, and relationships. "Owner" is the system of record; anything
owned by VPS Advisor is only *cached with provenance* here.

## Routing intelligence

| Entity | Owner | Description |
|---|---|---|
| **MonitoredProvider** | VPS Advisor | Provider eligible for monitoring: VPS Advisor provider ID, display name, ASNs, `monitoring_enabled`, priority. Synced, never authored here. |
| **ASN** | VPS Advisor (membership) / RIPE (facts) | An autonomous system tied to ≥1 MonitoredProvider. Carries registry name, country. A provider may have many ASNs; an ASN maps to exactly one provider (conflicts are an admin-visible error state, not silently resolved). |
| **Prefix** | Platform (derived) | An IPv4/IPv6 CIDR originated by a monitored ASN in a given routing snapshot. Attributes: origin ASN, first/last seen snapshot, GeoIP enrichment (country, city, coords), validation flags (bogon, MOAS conflict). |
| **RoutingSnapshot** | Platform | A versioned build: source RIS dump identity, build time, ASN/prefix counts, artifact checksum + signature, status (`building`→`published`→`superseded`/`failed`). |
| **ProbeTarget** | Platform (derived) | A concrete probeable address chosen from a provider's prefixes, carrying the country/city of the prefix it represents. Allocated country by country so a provider's whole footprint is measurable. Regenerated per snapshot; scheduling references targets, not raw prefixes. |
| **ProviderGeo** | Platform (derived) | A provider's announced address space in one country, for one snapshot: IPv4 address count, share of the provider's total, IPv6 `/64` count, contributing prefix counts, probe-target count. Counts *exclusive* space, so nested announcements are never counted twice. This is network **distribution**, not measurement. |

## Worker registry & trust

| Entity | Owner | Description |
|---|---|---|
| **Operator** | VPS Advisor | The human/community account that runs workers. Only its VPS Advisor ID is stored here. |
| **Worker** | Shared (enrolled on VPS Advisor, operated on platform) | A node: ID, operator ID, Ed25519 public key(s), state (`pending`, `active`, `suspended`, `quarantined`, `retired`), software version, self-reported + GeoIP-verified location, network (public IP's ASN — used to ensure measurement diversity and to *exclude a worker from measuring its own network*). |
| **WorkerKey** | Platform | A public key with validity window; supports rotation (old + new valid during overlap) and revocation. |
| **TrustScore** | Platform | Continuous score per worker (0–1) with component breakdown (consensus agreement, availability, tenure, anomaly penalties). Recomputed by the aggregator; history retained. |
| **TrustEvent** | Platform | Discrete events affecting trust or state: admin approval/suspension, consensus disagreement streak, invalid signature attempts, quarantine entry/exit. Append-only. |

## Scheduling & measurement

| Entity | Owner | Description |
|---|---|---|
| **Assignment** | Platform | An instruction: worker W probes target T with probe type P at interval I until expiry. Carries redundancy group ID linking the N workers covering the same target. |
| **Lease** | Platform | A worker's time-bounded claim on an assignment; renewal via heartbeat; expiry triggers reassignment. |
| **Observation** | Platform | One signed measurement result: worker, assignment, target, probe type, timestamps (worker + server receipt), typed metrics (`rtt_ms`, `packets_sent/lost`, per-protocol JSON), worker signature. Immutable, internal-only. |
| **ProbePolicy** | Platform (admin-set) | Global/per-provider limits: max probe rate per target, per worker, backoff on unreachable, forbidden target lists. Safety-critical: keeps the fleet from ever resembling abuse. |

## Aggregation & publication

| Entity | Owner | Description |
|---|---|---|
| **ConsensusWindow** | Platform | Trust-weighted aggregate for (provider, region, probe type, window): health verdict, confidence, latency percentiles, loss rate, contributing worker count, dissent ratio. `region` is `global` or the ISO 3166-1 country of the measured addresses (`ZZ` = unplaced). |
| **TargetStatus** | Platform | Current health of one monitored network: the probed address, its prefix/country/city, verdict, availability over the trailing window, latency, loss, contributing workers, last measurement time. |
| **ProviderStatus** | Platform → pushed to VPS Advisor | Current rollup per provider (+ per region): `healthy` / `degraded` / `outage` / `insufficient_data`, confidence, key metrics, active anomalies. |
| **Anomaly** | Platform | Detected event: type (reachability loss, latency regression, routing churn), scope, severity, opened/resolved timestamps, contributing evidence refs. |
| **PublicationRecord** | Platform | Outbox row for each push to the VPS Advisor Results API: payload hash, attempts, acked-at. Guarantees at-least-once delivery. |

## Key relationships

```
MonitoredProvider 1─* ASN 1─* Prefix *─1 RoutingSnapshot
Prefix 1─* ProbeTarget 1─* Assignment *─1 Worker
Assignment 1─* Observation *─consumed─* ConsensusWindow ─rollup→ ProviderStatus
Worker 1─1 TrustScore, 1─* TrustEvent, 1─* WorkerKey
ConsensusWindow ─feedback→ TrustScore
```

## Invariants

1. No entity in this platform stores provider business data beyond `provider_id`, name,
   ASNs, priority, and enabled flag.
2. A worker in any state other than `active` contributes zero weight to consensus and
   receives no assignments (quarantined workers *may* receive assignments whose results
   are recorded but weighted 0 — "shadow mode" — to earn back trust).
3. Every Observation is signed by a key valid for its worker at upload time; unsigned or
   invalidly signed data is rejected at the door and logged as a TrustEvent.
4. ProviderStatus is a pure function of ConsensusWindows — never of raw Observations.
5. A ProbeTarget must fall inside a prefix of the current published snapshot; assignments
   referencing targets from superseded snapshots are drained and reissued.
