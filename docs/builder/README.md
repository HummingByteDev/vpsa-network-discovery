# The RIPE Routing Builder

The **builder** is the component that turns raw global routing data into a
trustworthy, signed list of addresses for workers to probe. It is the only part
of VAPN that ever reads RIPE data or MaxMind databases. This guide explains it
from first principles — what the inputs are, what it does with them, and why —
then the [installation guide](installation.md) covers running one.

> **New to routing?** Read [Core Concepts](../concepts/README.md) first —
> especially [RIPE/RIS/MRT](../concepts/ripe-and-ris.md) and
> [prefix ownership](../concepts/prefix-ownership.md). This guide assumes those.

## What problem the builder solves

Workers need to know *exactly which addresses belong to each monitored
provider*, and they need to trust that list absolutely — a wrong entry means
probing someone else's network. But computing that list is expensive and
security-sensitive: it means downloading a ~1M-route global routing dump,
parsing a binary format, filtering, deduplicating, validating, and
geolocating.

The builder does all of that **once, centrally, and auditably**, then publishes
a tiny **signed** artifact. Thousands of workers consume the finished artifact;
none of them ever touches the raw data. This single boundary is the reason
workers can be a few MB of RAM and the routing logic can be improved without
redeploying the fleet.

```mermaid
flowchart LR
  subgraph Inputs
    A["Monitored ASNs<br/>(VPS Advisor)"]
    B["RIPE RIS bview<br/>(MRT dump)"]
    G["MaxMind GeoLite2<br/>(deployer's key)"]
  end
  A & B & G --> BUILD[Builder]
  BUILD --> PG[("PostgreSQL<br/>routing.* (canonical)")]
  BUILD --> ART["Signed SQLite artifact<br/>+ manifest"]
  ART --> STORE[(Artifact store)]
  STORE --> WK[Workers]
```

## The inputs, explained

### 1. The monitored ASN list (from VPS Advisor)

The builder asks VPS Advisor which providers/ASNs to monitor. This is the
*membership* input — it decides which slice of the global table to keep. VAPN
never scans the Internet; it only ever extracts prefixes for these ASNs.

### 2. The RIPE RIS `bview` dump (MRT)

This is the routing input: a full snapshot of the global routing table.

- **What RIPE is:** a [Regional Internet Registry](../concepts/ripe-and-ris.md#ripe-ncc-one-of-the-internets-registries)
  that, among other things, runs a free public recording of global routing.
- **What RIS is:** the [Routing Information Service](../concepts/ripe-and-ris.md#ris-the-routing-information-service) —
  collectors that passively record BGP announcements from many real networks.
- **What a `bview`/RIB dump is:** a periodic (every 8 h) full-table snapshot —
  "here is every route the collector currently sees."
- **What MRT is:** the binary file format (RFC 6396) the dump is stored in.
  Compact, standard, complete — and the reason a dedicated parser
  (`internal/routing/mrtreader`) exists.
- **Why MRT and not a live BGP feed:** VAPN wants *membership* ("which prefixes
  do these ASNs announce right now"), which a full-table snapshot answers
  directly, plus *churn* (from diffing snapshots) — neither needs the raw
  second-by-second update stream, and a snapshot is far simpler and safer to
  process.

### 3. MaxMind GeoLite2 (the deployer's own key)

The [GeoIP](../concepts/geoip.md) input — used to attach country/city/coords to
each prefix so verdicts can be regional. Each deployer supplies their own
MaxMind license key; the databases are never redistributed by the project, and
GeoIP refresh runs on an independent cadence.

## What the builder does, in order

This is [the snapshot-publishing walkthrough](../walkthroughs/snapshot-publishing.md)
in condensed reference form. Each numbered step maps to real code in
`internal/builder` and `internal/artifact`.

1. **Sync monitored ASNs** from VPS Advisor.
2. **Fetch the RIS `bview`** (or use the pre-downloaded `data/ripe/` copy in
   dev); reject it if older than `VAPN_RIS_BVIEW_MAX_AGE`.
3. **Parse + origin-filter**: stream the MRT, keep only prefixes originated by a
   monitored ASN. The big reduction (≈1M → a few thousand).
4. **Deduplicate**: collapse many per-peer reports of a `(prefix, origin AS)`
   into one fact. → [why duplicates exist](../concepts/prefix-ownership.md#complication-1-duplicate-prefixes)
5. **Validate**:
   - **Bogon filter** (`internal/routing/bogon`) — drop private/reserved/absurd
     prefixes that must never be probed.
   - **MOAS flagging** — mark ambiguous multi-origin prefixes for review rather
     than guessing ownership.
6. **Enrich with GeoIP** (`internal/routing/geo`) — country, city, coordinates.
7. **Load into `routing.*`** under a new `routing.snapshot` (status `building`).
   Prefixes are per-snapshot and immutable.
8. **Derive probe targets** — representative addresses per prefix/region, each
   with a recorded rationale, capped at `MAX_TARGETS_PER_PROVIDER`.
9. **Sanity gate** — if the prefix count swung more than
   `SANITY_MAX_DELTA_PCT` vs the previous snapshot, hold for admin approval
   (route-leak protection). `SANITY_FORCE=true` overrides.
10. **Export + sign** — write the worker-facing subset (`prefixes`, `targets`,
    `meta`) to a compact **SQLite** artifact and produce a **manifest** with
    counts, sha256, Ed25519 signature, and `min_worker_version`.
11. **Publish atomically** — upload to the artifact store, verify readback, mark
    `published`, previous → `superseded`. Any failure → `failed`, previous stays
    in force.

## Snapshot versioning, validation, and rollback

- **Versioning:** each snapshot has a monotonic version like
  `20260718T0800Z-1`. Workers refuse to downgrade to an older version.
- **Validation, two layers:** the *build-time* sanity gate (prefix-count swing)
  guards against poisoned input; the *consume-time* signature + sha256 check on
  every worker guards against a tampered or corrupt artifact.
- **Rollback:** if a bad snapshot slips through, an admin runs
  `vapnctl snapshots rollback <version>` to re-point `current` at a known-good
  snapshot — an audited action. Because publication is atomic, rollback is
  instantaneous from the workers' view.

## How workers consume the artifact

The published SQLite file contains only the worker-facing subset:

| Table | Columns (essentials) | Worker uses it to… |
|---|---|---|
| `prefixes` | `prefix, origin_asn, provider_id` | validate that a target is legitimate |
| `targets` | `address, provider_id, assignment hints` | know the probeable addresses |
| `meta` | `version, built_at, min_worker_version` | check compatibility + freshness |

Workers use it for **local validation and enrichment** — never to *choose*
targets (assignments come from the coordinator's scheduler). Download and
verification are covered in the
[worker authentication walkthrough](../walkthroughs/worker-authentication.md).

## Failure, recovery, and scalability

| Concern | Behavior |
|---|---|
| **RIS download fails / stale** | Build aborts; previous published snapshot stays fully in force |
| **Any build step fails** | Snapshot marked `failed`; atomic publish means workers never see a partial build |
| **Poisoned routing data (leak/hijack)** | Sanity gate holds anomalous builds for a human; bogon/MOAS validation drops/flags bad prefixes |
| **Bad snapshot published** | `vapnctl snapshots rollback` re-points to a good version (audited) |
| **Artifact store compromised** | Store is untrusted; signature verification on every worker catches tampering |
| **Fleet growth** | Artifact distribution is CDN-offloadable from day one; the builder's cost is independent of worker count |

The builder is a **batch job, not a daemon** — it runs, publishes, and exits, so
it has no inbound network surface and scales trivially (run it on a schedule).

## Future extensions

- Additional RIS collectors / cross-collector agreement for stronger ownership
  signals.
- RPKI validation (is this origin cryptographically authorized for this prefix?)
  layered onto the existing MOAS/bogon validation.
- Richer target selection (per-region representative sampling) behind the same
  `targets` table shape — no worker or schema change needed.

To run one, continue to [Builder Installation](installation.md). For the design
record, see [architecture 01 §4.1](../architecture/01-system-architecture.md#41-snapshot-builder-builder).
