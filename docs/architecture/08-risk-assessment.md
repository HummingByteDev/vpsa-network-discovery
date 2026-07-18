# 08 — Risk Assessment

Ranked by (likelihood × impact) on the project's goal: trustworthy public provider
health data. Security threats are enumerated in [05](05-security-trust-model.md) §3;
this covers project/architecture risk.

## High

**R1 — Measurement validity: ICMP to a few addresses ≠ provider health.**
A provider may deprioritize ICMP, anycast, or rate-limit probes; a healthy network can
look degraded and vice versa. *Mitigations:* multiple targets per provider spread across
prefixes; verdicts require cross-worker + cross-region corroboration; conservative
`insufficient_data` posture; protocol-agnostic engine so TCP/HTTP probes can corroborate
in a later phase; publish confidence alongside verdicts so the website never overstates.
*Residual:* v1 verdicts are "network reachability" claims, not SLA claims — wording on
the site must reflect this (integration guide item).

**R2 — Probing without consent looks like abuse.** *(Resolved 2026-07-18.)*
Decision: provider profile managers on VPS Advisor can opt out at any time via
`monitoring_enabled` — no commitment, immediate effect (scheduler drains within one sync
cycle). Remaining technical mitigations stay in force: strict ProbePolicy rate caps
(target-level, far below any nuisance threshold), documented probe pattern, global and
per-provider kill switches.

**R3 — Cold-start chicken-and-egg: consensus needs many trustworthy workers; trust
needs consensus history.** With <10 workers, redundancy and Sybil resistance are weak.
*Mitigations:* launch with first-party "anchor" workers run by VPS Advisor in a few
regions (full trust, clearly labeled internally); community workers add diversity on
top; verdict thresholds scale with fleet size; do not publish per-region verdicts for
regions lacking diversity.

**R4 — VPS Advisor integration slips.** The site team must build A1–A4 endpoints;
platform progress blocks on them. *Mitigations:* mock VPS Advisor stub from Phase 1
(fixture-driven, contract-tested); contracts frozen early ([04](04-api-contracts.md));
integration guide written for the site team by Phase 9, but the stub keeps this repo
shippable and testable independently.

## Medium

**R5 — RIS data quality.** Route leaks, MOAS, stale bview → wrong prefixes → probing
someone else's network (overlaps R2). *Mitigations:* validation gates, diff thresholds
with human hold, bogon filters, target selection biased to long-lived prefixes.

**R6 — Measurement write volume outgrows single Postgres.** 1000 workers × frequent
probes is fine with batching + partitions, but 10× that isn't guaranteed.
*Mitigations:* batch/COPY ingestion, short raw retention, rollups; seam for moving raw
observations to a specialized store later (Timescale/ClickHouse) isolated behind the
`measurements` schema — aggregation reads via one interface.

**R7 — Community operator experience.** If enrollment or upgrades are fiddly, the fleet
never materializes. *Mitigations:* one-env-var setup, self-diagnosing worker (`worker
doctor` command), status visibility on the operator dashboard, docs treated as a
first-class deliverable (per brief).

**R8 — GeoLite2 licensing/redistribution.** *(Resolved 2026-07-18.)*
Decision: the platform never redistributes MaxMind databases. Every party that hosts a
component needing GeoIP (builder deployers; worker operators, optionally) supplies their
own MaxMind license key and fetches GeoLite2 directly from MaxMind. Workers without a
key degrade gracefully — geo enrichment for workers is optional since authoritative
enrichment happens in the builder and ships inside our own snapshot artifact (derived
fields only, which carries no redistribution constraint).

**R9 — Clock skew on community hardware.** Signed timestamps + measurement windows
assume sane clocks. *Mitigations:* server-authoritative `received_at`, skew measured at
heartbeat and reported, tolerance windows, workers with gross skew flagged.

## Low

**R10 — MRT parsing edge cases** (add-path, RIB size growth): use a maintained library,
golden-file tests against the real bview in `data/`.
**R11 — Artifact store outage:** workers keep last verified snapshot; only blocks
*updates*, not measurement.
**R12 — Postgres as scheduler bottleneck** (lease contention): lease batching and
`SKIP LOCKED` patterns; proven at far larger scales than ours.

## Explicit non-risks (scoped out)

BGP hijack detection, per-customer-VM monitoring, active traceroute topology mapping —
valuable future work; the schemas leave room (probe_type, metrics JSONB, anomaly kinds)
but v1 makes no promises.
