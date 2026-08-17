# Glossary

Every networking and project-specific term used in this documentation, defined
in plain language. Networking terms link to the concept page that teaches them.

## Networking

**AS (Autonomous System)** — an independently operated network (an ISP, cloud,
or hosting company) that makes its own routing decisions. →
[ASN & BGP](../concepts/asn-and-bgp.md)

**ASN (Autonomous System Number)** — the globally unique number identifying an
AS, e.g. `AS64500`. A provider *is*, on the Internet, its set of ASNs. →
[ASN & BGP](../concepts/asn-and-bgp.md)

**BGP (Border Gateway Protocol)** — the protocol ASes use to announce which
address prefixes they can reach; it's what glues the Internet's networks
together. → [ASN & BGP](../concepts/asn-and-bgp.md)

**bogon** — an address block that must never appear in public routing (private,
reserved, or absurd). The builder drops these so they can't become targets. →
[Prefix ownership](../concepts/prefix-ownership.md)

**`bview` / RIB dump** — a full-table snapshot of the routing table, taken by a
RIS collector every 8 hours. VAPN builds from these. →
[RIPE/RIS/MRT](../concepts/ripe-and-ris.md)

**CIDR** — notation for an address block: `address/prefix-length`, e.g.
`203.0.113.0/24` (256 addresses). Smaller number after the slash = bigger block.
→ [How the Internet works](../concepts/how-the-internet-works.md)

**GeoIP** — mapping IP addresses to approximate geographic locations and to the
registered network. VAPN uses MaxMind GeoLite2. → [GeoIP](../concepts/geoip.md)

**ICMP echo (ping)** — the request/reply used to test reachability and measure
round-trip time; VAPN's first probe type. →
[Measurement](../concepts/measurement-and-consensus.md)

**IP address** — the numeric label identifying a device on the network
(`203.0.113.7`). IPv4 (32-bit) and IPv6 (128-bit). →
[How the Internet works](../concepts/how-the-internet-works.md)

**MOAS (Multiple Origin AS)** — a prefix announced by more than one origin AS —
ambiguous ownership. VAPN flags these rather than guessing. →
[Prefix ownership](../concepts/prefix-ownership.md)

**MRT** — the binary file format (RFC 6396) that RIS routing recordings are
stored in. Only the builder parses it. → [RIPE/RIS/MRT](../concepts/ripe-and-ris.md)

**origin AS** — the AS that originates a prefix's announcement — its owner, for
VAPN's purposes. → [ASN & BGP](../concepts/asn-and-bgp.md)

**prefix** — an announced address block; a provider's "streets." VAPN reasons
about prefixes, not individual addresses. →
[How the Internet works](../concepts/how-the-internet-works.md)

**RIB (Routing Information Base)** — a router's table of all known routes;
captured as a `bview` dump. → [RIPE/RIS/MRT](../concepts/ripe-and-ris.md)

**RIPE NCC** — the Regional Internet Registry for Europe; runs the RIS service
VAPN reads routing data from. → [RIPE/RIS/MRT](../concepts/ripe-and-ris.md)

**RIR (Regional Internet Registry)** — one of five bodies (RIPE, ARIN, APNIC,
LACNIC, AFRINIC) that allocate addresses and ASNs. →
[RIPE/RIS/MRT](../concepts/ripe-and-ris.md)

**RIS (Routing Information Service)** — RIPE's network of passive collectors that
record global BGP announcements. → [RIPE/RIS/MRT](../concepts/ripe-and-ris.md)

**route collector / rrc** — a RIS machine that records BGP without announcing
anything; VAPN defaults to `rrc00`. → [RIPE/RIS/MRT](../concepts/ripe-and-ris.md)

**route propagation** — how BGP announcements/withdrawals spread across the
Internet over seconds to minutes. → [ASN & BGP](../concepts/asn-and-bgp.md)

**RTT (round-trip time)** — the latency of a probe, in milliseconds. →
[How the Internet works](../concepts/how-the-internet-works.md)

**reachability** — whether a packet gets to a target and back at all. →
[Measurement](../concepts/measurement-and-consensus.md)

## VAPN concepts

**aggregation engine (`aggregator`)** — the service that computes trust-weighted
[consensus](../concepts/measurement-and-consensus.md#consensus-from-many-views-to-one-verdict), detects
anomalies, updates trust, and publishes results.

**anomaly** — a detected reachability loss, latency regression, or routing
churn, opened/resolved by the aggregator. →
[Measurement](../concepts/measurement-and-consensus.md#detecting-anomalies)

**artifact / snapshot artifact** — the compact, signed **SQLite** file (plus
manifest) that workers download; the worker-facing subset of a routing snapshot.
→ [Builder](../builder/README.md)

**assignment** — an instruction to a worker: probe target T with probe type P at
interval I. → [Measurement lifecycle](../walkthroughs/measurement-lifecycle.md)

**builder (Snapshot Builder)** — the batch service that turns RIPE data into a
signed target list; the only component that reads MRT/MaxMind data. →
[Builder](../builder/README.md)

**confidence** — how sure a verdict is (higher with more distinct workers and
less dissent). → [Measurement](../concepts/measurement-and-consensus.md#consensus-from-many-views-to-one-verdict)

**consensus** — the trust-weighted combination of a redundancy group's
measurements into a verdict. →
[Measurement](../concepts/measurement-and-consensus.md#consensus-from-many-views-to-one-verdict)

**consensus window** — a fixed time bucket (default 5 min) over which consensus
is computed. → [Trust calculation](../walkthroughs/trust-calculation.md)

**control plane / data plane** — the split between VPS Advisor (identity,
catalog, enrollment, admin — control) and this platform's high-volume
worker-facing Coordinator API (data). →
[Architecture 01 §2](../architecture/01-system-architecture.md#2-api-plane-split-clarification-of-the-brief)

**coordinator** — the long-running HTTP service workers talk to: auth,
heartbeats, scheduling, observation intake. →
[Architecture 01 §4.2](../architecture/01-system-architecture.md#42-coordinator-coordinator)

**Ed25519** — the public-key signature scheme workers use to sign requests and
observations, and the builder uses to sign artifacts. →
[Worker authentication](../walkthroughs/worker-authentication.md)

**enrollment token** — a one-time secret proving a real operator started a
worker; traded for a permanent identity on first boot. →
[Install a worker](../worker/installation.md#what-vapn-install-actually-does)

**heartbeat** — the ~30 s worker→coordinator call carrying liveness/version and
returning config, leases, snapshot version, and control actions. →
[Measurement lifecycle](../walkthroughs/measurement-lifecycle.md)

**insufficient_data** — the verdict when too few distinct trusted workers
measured a provider; shown as "not enough data," never an outage. →
[Measurement](../concepts/measurement-and-consensus.md#consensus-from-many-views-to-one-verdict)

**lease** — a worker's time-bounded claim on an assignment, renewed by
heartbeat; expiry triggers reassignment. →
[Measurement lifecycle](../walkthroughs/measurement-lifecycle.md)

**observation** — one signed measurement result from one worker; internal-only,
never public on its own. →
[Measurement](../concepts/measurement-and-consensus.md#what-a-single-measurement-is)

**operator** — the human/community account (on VPS Advisor) that runs workers.

**probe engine / prober** — the protocol-agnostic component inside the worker;
ICMP is the first `Prober` implementation. →
[Measurement lifecycle](../walkthroughs/measurement-lifecycle.md)

**network distribution** — where a provider's announced IPv4 space *is*, by
country, as a share of its deduplicated total. Derived from BGP and GeoIP, so it
exists whether or not anyone has measured the provider — the opposite of a
monitoring result. → [GeoIP](../concepts/geoip.md#enriching-prefixes-builder)

**probe target** — a representative address chosen from a prefix; what workers
actually probe. Carries the country of its prefix, and targets are allocated
country by country so a provider's whole footprint is measurable. →
[Prefix ownership](../concepts/prefix-ownership.md)

**quarantine / shadow mode** — a lifecycle state where a worker measures at
weight 0 to rebuild trust. → [Worker lifecycle](../worker/lifecycle.md#quarantined-shadow-mode)

**region** — in verdicts and the status document, the ISO 3166-1 alpha-2 country
of the **measured address** (not of the worker measuring it), or `global`. `ZZ`
means address space the GeoIP database does not place, and is rendered
"unknown", never as a country. → [GeoIP](../concepts/geoip.md#regional-verdicts-made-concrete)

**redundancy group** — the set of independent workers covering the same target,
spread across regions/networks/operators. →
[Measurement](../concepts/measurement-and-consensus.md#the-fix-community-measurement--redundancy)

**replay protection** — timestamp window + per-worker nonce uniqueness that stops
captured requests from being reused. →
[Worker authentication](../walkthroughs/worker-authentication.md)

**routing snapshot** — an immutable, versioned record of prefix ownership at
build time; the source of the worker artifact. →
[Builder](../builder/README.md#one-build-stage-by-stage)

**sanity gate** — the builder's hold-for-approval check when a snapshot's prefix
count swings past a threshold (route-leak protection). →
[Builder](../builder/README.md)

**scheduler** — the coordinator module that turns targets into assignments and
leases them with a redundancy factor. →
[Architecture 01 §4.2](../architecture/01-system-architecture.md#42-coordinator-coordinator)

**source ASN** — the network a worker probes *from*; used for measurement
diversity and to stop a worker measuring its own provider. →
[GeoIP](../concepts/geoip.md)

**Sybil attack** — one actor running many fake nodes to sway results; blunted by
manual approval, the tenure ramp, and per-operator weight caps. →
[Security & trust model](../architecture/05-security-trust-model.md)

**trust** — a worker's [0,1] reliability score; its weight in consensus. →
[Measurement](../concepts/measurement-and-consensus.md#trust)

**verdict** — a provider's health state: `healthy`, `degraded`, `outage`, or
`insufficient_data`. → [Measurement](../concepts/measurement-and-consensus.md#consensus-from-many-views-to-one-verdict)

**VPS Advisor** — the independent, already-live review website that is the source
of truth for providers and the consumer of VAPN's results. **This repository is
not VPS Advisor.** → [Documentation Home](../README.md#project-background)

**worker** — a community-run container that probes targets and uploads signed
measurements. → [Community Workers](../worker/README.md)

**`vapn` / `vapnctl`** — the worker-operator CLI and the platform-admin CLI. →
[CLI reference](cli.md)
