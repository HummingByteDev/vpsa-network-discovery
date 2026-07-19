# The VAPN Project Handbook

*A complete guide to the VPS Advisor Probe Network — from Internet fundamentals
to production operation.*

---

This handbook tells the whole story of VAPN in one place, in order, as a book.
It starts by assuming you know how to write software but have never worked with
Internet routing, builds the necessary foundations, and then introduces every
subsystem until you understand the entire platform — why it exists, how it
works, and how to run it.

It is designed to be read cover to cover and to be exported as a PDF. Where a
topic has a dedicated reference elsewhere in the documentation, this handbook
teaches the essentials inline and links out for exhaustive detail, so you never
*need* to leave — but can when you want more.

**How to read it.** If you're new, read straight through. If you know
networking, skim Part I and start at [Chapter 6](#chapter-6--project-goals). If
you're here for one subsystem, use the table of contents. Every chapter ends
knowing what the next one needs.

## Table of contents

**Part I — Foundations**
1. [Introduction](#chapter-1--introduction)
2. [Internet Fundamentals](#chapter-2--internet-fundamentals)
3. [Autonomous Systems & ASNs](#chapter-3--autonomous-systems--asns)
4. [BGP: How Networks Find Each Other](#chapter-4--bgp-how-networks-find-each-other)
5. [RIPE, RIS & MRT](#chapter-5--ripe-ris--mrt)

**Part II — The System**
6. [Project Goals](#chapter-6--project-goals)
7. [System Architecture](#chapter-7--system-architecture)
8. [Component Walkthrough](#chapter-8--component-walkthrough)
9. [The Builder](#chapter-9--the-builder)
10. [Workers](#chapter-10--workers)
11. [Aggregation & Consensus](#chapter-11--aggregation--consensus)
12. [Trust & Security](#chapter-12--trust--security)
13. [VPS Advisor Integration](#chapter-13--vps-advisor-integration)

**Part III — Running It**
14. [Deployment](#chapter-14--deployment)
15. [Operations](#chapter-15--operations)
16. [Development](#chapter-16--development)
17. [Troubleshooting](#chapter-17--troubleshooting)
18. [Future Roadmap](#chapter-18--future-roadmap)

---

# Part I — Foundations

# Chapter 1 — Introduction

Imagine you're choosing a hosting provider. Reviews tell you about support and
price, but they can't answer the question that matters once your site is live:
**is this provider's network actually reliable — and reliable from where my
users are?** Traditional uptime monitors can't help either: they watch a server
*you already own*, not a provider you're *considering*.

VAPN — the **VPS Advisor Probe Network** — exists to answer that question
honestly. It's a distributed system in which a global community runs small
programs called **workers** that measure the public network health of VPS
providers from many vantage points. No single worker is trusted on its own;
instead the platform combines many independent measurements into a
**consensus** verdict, weights each worker by how reliable it has proven to be
(**trust**), and publishes only the agreed-upon result to the VPS Advisor
website for the world to see.

Three things make VAPN different from an uptime checker:

- **It measures providers, not your servers.** The target is a provider's own
  *public network* — the address blocks it announces to the Internet.
- **It's independent and communal.** Measurements come from strangers around the
  world, not from the provider (who would grade their own homework) or a single
  biased vantage point.
- **It assumes the measurers might lie.** Cryptographic signing, redundancy,
  consensus, and a trust system make the *aggregate* trustworthy even when
  individual participants aren't.

> **A crucial boundary, stated once and for all.** VPS Advisor is an
> independent, already-live provider-review website. **VAPN is not that website.**
> VAPN is the measurement backend behind one of its features (Provider Network
> Health). VPS Advisor decides *which* providers exist and *which* networks they
> own; VAPN measures them and reports back. Two systems, one clean contract.

To understand how VAPN measures a "provider's public network," you first need a
little of the networking that VAPN is built on. That's Part I. If you already
know IP, CIDR, ASN, BGP, and RIPE, skip to
[Chapter 6](#chapter-6--project-goals).

# Chapter 2 — Internet Fundamentals

The Internet is best understood as a **global postal system**, and we'll use
that analogy throughout Part I.

**Every device online has an IP address** — a numeric label, like a building's
postal address. IPv4 addresses look like `203.0.113.7` (32 bits, ~4.3 billion of
them); IPv6 like `2001:db8::7` (128 bits, effectively unlimited). VAPN handles
both.

**Addresses are allocated in blocks**, written in **CIDR** notation:
`203.0.113.0/24`. The number after the slash — the *prefix length* — says how
many leading bits are fixed as the "network part." A `/24` fixes 24 bits and
leaves 8, so it's 256 addresses; a `/16` is 65,536; a `/32` is a single address.
**Smaller number after the slash means a bigger block.** In postal terms,
`203.0.113.0/24` is "all 256 doors on Maple Street," and `203.0.113.7/32` is
"7 Maple Street." Routing happens at the *street* level, which is why blocks
matter.

In VAPN's language, an announced address block is a **prefix**. A hosting
provider owns a handful of prefixes — collectively, its *public network*.

**How does a packet reach an address?** No router has a map of the whole
Internet. Each one keeps a *routing table* — "for this block of addresses, hand
the packet to that neighbor" — and forwards each packet one hop closer. The
packet hops router to router, network to network, each making a purely local
decision, until it reaches a router inside the destination's network. Just like
a letter hops sorting hub to sorting hub, each hub knowing only "mail for that
region goes this way."

Two properties of that journey are what VAPN measures:

- **Reachability** — does a packet get there and back at all?
- **Latency (RTT)** — how many milliseconds the round trip takes.

A worker measures both by sending an **ICMP echo request** (a "ping") and timing
the reply. Simple — but a single ping from a single place is a weak signal,
which is the problem the rest of the system exists to solve.

The open question this raises: *how does a router learn "for block X, send toward
network Y"?* That requires knowing what the networks are (Chapter 3) and how they
tell each other what they can reach (Chapter 4).

> Full treatment with diagrams: [How the Internet works](../concepts/how-the-internet-works.md).

# Chapter 3 — Autonomous Systems & ASNs

"The Internet" isn't one network; it's tens of thousands of independent networks
that agree to interconnect. Each one — an ISP, a cloud, a university, a hosting
company — is an **Autonomous System (AS)**: a black box run by one organization
that makes its own internal routing decisions.

Every AS has a globally unique number, its **ASN**, written like `AS64500`. In
our analogy, an **AS is a courier company** and its ASN is the company number:
each courier controls delivery in its own territory and hands packages to other
couriers at the borders.

This is the linchpin for VAPN. A hosting provider you'd review on VPS Advisor
*is*, on the Internet, **its ASN (or ASNs) and the address blocks those ASNs
announce.** That's why VAPN's source of truth is ASNs: VPS Advisor records "this
provider owns AS64500," and VAPN's job becomes "find every prefix AS64500
announces, and measure it."

A provider may own several ASNs; an ASN belongs to exactly one provider. VAPN
enforces that last rule strictly — an ASN claimed by two providers is an error a
human must resolve, never something VAPN guesses about.

> Full treatment: [ASN & BGP](../concepts/asn-and-bgp.md).

# Chapter 4 — BGP: How Networks Find Each Other

Given thousands of autonomous networks, how does any one reach any other? They
exchange reachability information using the **Border Gateway Protocol (BGP)** —
the routing protocol *between* Autonomous Systems, and quite literally what glues
the Internet together.

The mechanic: an AS **announces** the prefixes it can deliver to, with the **AS
path** showing the route. AS64500 announces `203.0.113.0/24` with path
`[64500]` — "I originate this block; come to me." Its neighbors re-announce it,
each prepending itself: `[100, 200, 64500]` means "reach it via AS200, which
reaches AS64500." Every router that hears this learns a *direction* toward the
block, without any router holding a global map. Routing is **rumor that
converges** — each courier shouts "I can deliver to Maple Street!" and neighbors
relay the shout, adding "…through me."

The AS at the end of the path — the one that actually originates the
announcement — is the **origin AS**. For VAPN, the origin AS *is* ownership: if
AS64500 is a monitored provider, every prefix originated by AS64500 is part of
that provider's public network.

Two BGP realities matter later:

- **Announcements propagate over seconds to minutes**, and can be **withdrawn**.
  An unexpected withdrawal means parts of the Internet lose the path to a
  provider — a real outage, even if the provider's servers are fine. Repeated
  announce/withdraw (**flapping**) means instability.
- **A prefix can have multiple origin ASes at once** — a **MOAS**. Sometimes
  legitimate, sometimes a hijack or leak. Ambiguous ownership that VAPN must
  handle carefully, never guess.

VAPN doesn't speak BGP itself. It reads *recordings* of these announcements —
which is Chapter 5.

> Full treatment: [ASN & BGP](../concepts/asn-and-bgp.md).

# Chapter 5 — RIPE, RIS & MRT

Someone has to allocate addresses and ASNs so they don't collide; that's split
by region among five **Regional Internet Registries**. **RIPE NCC** is the
European one, and — crucially for VAPN — it runs a free, public, global recording
of BGP called the **Routing Information Service (RIS)**.

RIS is a network of **route collectors** — machines that peer with many real
networks and passively *listen* to their BGP announcements, recording everything
and announcing nothing. A tape recorder for global routing. Each collector
produces periodic **`bview` dumps**: full snapshots of the entire routing table
(about a million routes), taken every 8 hours, stored in the **MRT** format — the
standard binary container for routing data (RFC 6396).

VAPN builds from these `bview` dumps because it needs *membership* ("which
prefixes do these ASNs announce right now?"), which a full-table snapshot answers
directly, and *churn* (which it gets by diffing successive snapshots) — neither
needs the raw second-by-second update stream. A dump is far simpler and safer to
process.

One dump is ~1M binary records and tens to hundreds of MB. VAPN reads the whole
thing but **keeps only the tiny slice** originated by monitored ASNs — it never
builds a database of the entire Internet. And it does this parsing **exactly
once, centrally**, in the builder; workers never touch MRT. This single boundary
is why workers can be a few MB of RAM and why the routing logic can be improved
without redeploying the fleet.

> Full treatment: [RIPE/RIS/MRT](../concepts/ripe-and-ris.md). To make
> development offline-friendly, the repo ships a pre-downloaded dump and MaxMind
> databases under `data/`.

With the foundations in place, we can now describe what VAPN actually does.

---

# Part II — The System

# Chapter 6 — Project Goals

VAPN's purpose is to answer, trustworthily and at scale:

> *Is this provider's public network healthy — globally, and in my region — right
> now, and how has it behaved recently?*

Concretely, the project must:

- Measure the **public network health** of providers listed on VPS Advisor,
  using only the routes those providers announce.
- Use **community-operated** workers from many vantage points.
- Produce **trust-weighted, consensus** verdicts — never expose a single
  worker's raw opinion.
- Detect **anomalies** (reachability loss, latency regressions, routing churn).
- Push results back to VPS Advisor for display.

And it must do so under hostile assumptions — **malicious workers exist,
credentials get stolen, measurements are unreliable** — while staying:
scalable (tens of thousands of providers, thousands of workers), maintainable,
secure, modular, observable, and well-documented.

Equally important is what VAPN **does not** do: it doesn't scan the Internet for
providers, doesn't maintain its own provider registry, and doesn't touch
provider profiles, reviews, accounts, or billing. VPS Advisor owns all of that.
VAPN is measurement; VPS Advisor is truth-of-record and presentation.

> Full brief: [`CLAUDE.md`](../../CLAUDE.md). Architecture rationale:
> [architecture 01 §1](../architecture/01-system-architecture.md).

# Chapter 7 — System Architecture

VAPN is deliberately split from VPS Advisor along a **control-plane / data-plane**
line — the one clarification the design made to the original brief:

- **Control plane — VPS Advisor** (existing site, extended): human-facing,
  low-volume — operator accounts, worker enrollment and approval, the provider
  catalog, monitoring configuration, the admin dashboard, and ingestion of
  aggregated results.
- **Data plane — VAPN** (this repository): machine-facing, high-volume — worker
  heartbeats, assignment leases, observation uploads, snapshot downloads.

Why split them? Thousands of workers probing every few seconds must never hit the
production review website. The split gives volume isolation, independent scaling,
and a small change surface on the existing site. Workers are *logically*
registered with VPS Advisor (accounts and approval live there) but *operationally*
talk only to VAPN's coordinator.

```mermaid
flowchart TB
  VA["VPS Advisor Website (control plane)<br/>catalog · accounts · enrollment · admin · Results API"]
  subgraph VAPN [VAPN platform (data plane)]
    B[Snapshot Builder] --> PG[(PostgreSQL)]
    CO[Coordinator + Scheduler] --> PG
    AG[Aggregation Engine] --> PG
    B --> AS[(Artifact Store)]
  end
  VA -->|provider/ASN pull| VAPN
  VAPN -->|aggregated results push| VA
  AS -->|signed snapshots| WK[Community Workers]
  WK -->|signed observations| CO
```

Technology choices in brief: **Go** for every service and the worker (single
static binaries, tiny community Docker images, good raw-socket support);
**PostgreSQL 16+** everywhere server-side (partitioning, `cidr`/`inet` types,
LISTEN/NOTIFY); **SQLite** for the read-only artifact workers download;
**Ed25519** for worker request signing and artifact signing.

The system is built to be *correct* at hundreds of workers and *not structurally
blocked* from tens of thousands: the coordinator is stateless (scale
horizontally), measurements are partitioned and batched, artifact distribution is
CDN-offloadable from day one, and aggregation is parallel by provider.

> Full treatment: [architecture 01](../architecture/01-system-architecture.md).

# Chapter 8 — Component Walkthrough

Five components, each with a sharp boundary. This chapter names them; the next
chapters go deep on the interesting ones.

- **Snapshot Builder** (`builder`) — a *batch job* that owns routing
  intelligence. Pulls the monitored-ASN list, downloads RIPE RIS data, extracts
  and validates each provider's prefixes, enriches with GeoIP, and publishes a
  signed SQLite artifact. The only component that reads MRT or MaxMind data.
  → [Chapter 9](#chapter-9--the-builder)
- **Coordinator** (`coordinator`) — a *long-running service*, the single endpoint
  workers talk to. Authenticates workers, enforces lifecycle, runs the embedded
  **scheduler** (targets → assignments → leases with redundancy), ingests signed
  observations, serves snapshot metadata. Never computes public status; never
  decides which providers are monitored.
- **Aggregation Engine** (`aggregator`) — a *long-running service* that owns
  consensus and public truth. Windows observations, computes trust-weighted
  verdicts and confidence, detects anomalies, updates trust, and pushes results
  to VPS Advisor via an outbox. → [Chapter 11](#chapter-11--aggregation--consensus)
- **Worker** (`worker`) — a *community container* with minimal config. Generates
  a keypair, registers, downloads and verifies snapshots, leases assignments,
  probes, signs and uploads observations, self-updates. The probe engine is
  protocol-agnostic. → [Chapter 10](#chapter-10--workers)
- **Artifact Store** — dumb, cacheable HTTPS storage for snapshots. Untrusted by
  design (integrity comes from signatures), so it can sit behind a CDN.

Behind them all: **PostgreSQL** (six schemas — routing, registry, scheduling,
measurements, aggregation, audit) and the **VPS Advisor** control plane.

The end-to-end flow that ties them together — provider added → ASN synced →
snapshot built → workers probe → consensus computed → results published — is
told stage by stage in the
[end-to-end walkthrough](../walkthroughs/end-to-end.md).

# Chapter 9 — The Builder

The builder solves one hard, sensitive problem: **produce a trustworthy,
signed list of exactly which addresses belong to each monitored provider.** Get
it wrong and workers probe someone else's network.

Each run, on a daily schedule:

1. **Sync monitored ASNs** from VPS Advisor.
2. **Download the RIS `bview`** MRT dump (or use the pre-downloaded dev copy).
3. **Parse and origin-filter** — stream ~1M records, keep only prefixes
   originated by a monitored ASN (the big reduction).
4. **Deduplicate** — a dump reports each prefix from many peers; collapse to one
   fact per `(prefix, origin AS)`.
5. **Validate** — drop **bogons** (private/reserved/absurd prefixes that must
   never be probed) and **flag MOAS conflicts** for review rather than guessing
   ownership.
6. **Enrich with GeoIP** — attach country/city/coordinates from MaxMind
   GeoLite2, so verdicts can later be regional.
7. **Load into PostgreSQL** under a new immutable snapshot version.
8. **Derive probe targets** — a small, capped set of representative addresses per
   prefix (not every address in a block, which would look like a scan).
9. **Sanity-gate** — if the prefix count swung wildly versus the previous
   snapshot, *hold for admin approval* (a route leak could be poisoning the
   data).
10. **Export and sign** — write the worker-facing subset to a compact SQLite
    artifact and sign a manifest (counts, sha256, Ed25519 signature,
    min worker version).
11. **Publish atomically** — upload, verify readback, mark `published`, previous
    → `superseded`. Any failure marks the build `failed` and leaves the previous
    version fully in force; workers never see a half-built snapshot.

Two validation layers protect the fleet: the *build-time* sanity gate (bad input)
and the *consume-time* signature check every worker performs (bad or tampered
artifact). If a bad snapshot ever slips through, an admin runs
`vapnctl snapshots rollback <version>` and, because publishing is atomic,
workers switch to the good version on their next heartbeat.

> Full treatment: [The Routing Builder](../builder/README.md); the run in
> detail: [snapshot publishing](../walkthroughs/snapshot-publishing.md);
> the ownership logic: [prefix ownership](../concepts/prefix-ownership.md).

# Chapter 10 — Workers

A worker is a small Docker container run by a community member. It's intentionally
minimal — a few MB of RAM, negligible CPU, a trickle of bandwidth, no inbound
ports — because all the heavy lifting lives in the builder and coordinator.

**Its life begins with enrollment.** The operator creates a worker on VPS Advisor
and receives a one-time **enrollment token**. On first boot the worker generates
an **Ed25519 keypair** — the private key never leaves the machine — registers with
the coordinator using the token and its public key, and enters the `pending`
state. A human admin then approves it (an anti-abuse gate), and it becomes
`active`.

**Its steady state is a loop.** Every ~30 seconds it heartbeats, receiving config,
control actions, the current snapshot version, and lease renewals. It downloads
and cryptographically verifies new snapshots (rejecting anything whose signature
or hash doesn't match its pinned key). It leases **assignments** from the
scheduler — never choosing its own targets — probes each on its interval (ICMP
echo in v1), batches the results, **signs each observation and the batch**, and
uploads. If the coordinator is down, it queues locally and retries; on shutdown it
releases its leases cleanly.

**Every request it makes is signed**, carrying a worker id, timestamp, nonce, and
Ed25519 signature over the method, path, and body hash. This proves the request
came from that worker, wasn't altered, and isn't a replay — on top of HTTPS.

**The operator stays in control** through a small CLI: `vapn status`, `logs`,
`pause`/`resume`, `update` (health-gated with automatic rollback),
`unregister`, `uninstall`. Everything else — credential renewal, snapshot
downloads, retries, reboot recovery — the worker does itself.

The probe engine is **protocol-agnostic**: ICMP is the first implementation of a
`Prober` interface, and TCP-connect, traceroute, and HTTP(S) checks can be added
behind the same interface without changing scheduling, upload, or aggregation.

> Full treatment: [Community Workers](../worker/README.md), the
> [lifecycle](../worker/lifecycle.md), and the
> [measurement](../walkthroughs/measurement-lifecycle.md) and
> [authentication](../walkthroughs/worker-authentication.md) walkthroughs.

# Chapter 11 — Aggregation & Consensus

This is where many shaky individual measurements become one verdict you can rely
on. The aggregation engine's guiding posture is **conservative**: it would rather
say "not enough data" than cry wolf.

**Why a single measurement can't be trusted.** A measurement is a statement about
a *path*, from one worker to one target — not about the provider. That path can
be broken (the worker's local ISP has a problem) or privileged (the worker sits in
the provider's own data center) for reasons unrelated to the provider. And ICMP
has quirks: some hosts deliberately drop pings, so silence isn't necessarily an
outage.

**The fix is redundancy plus consensus.** Every target is measured by several
workers (default 3), deliberately spread across different regions and source
networks, and never by a worker on the provider's own network. Each window
(default 5 minutes), the engine:

1. Reduces each worker's observations of a target to an ok-ratio.
2. Has workers **vote by trust weight**; a target is **up** if ≥ 50% of weight
   saw it reachable. Only **responsive targets** (that answered *someone*
   recently) count — a never-answering address is a non-signal, not an outage.
3. Rolls targets up to a provider **verdict** by the up-fraction: **healthy**
   (≥ 90%), **degraded** (≥ 50%), **outage** (< 50%), or **insufficient_data**
   (fewer than 3 distinct workers, or nothing measurable).
4. Attaches **confidence** (higher with more workers, less dissent) and latency
   percentiles.

Two rules are sacred: **`insufficient_data` is a real, honest outcome** (rendered
as "not enough data," never an outage), and **the public verdict is a pure
function of consensus, never of raw observations** — no single worker's report is
ever published.

The same pipeline detects **anomalies** — a transition into degraded/outage opens
a reachability anomaly; a latency p50 ≥ 2× the 6-hour baseline opens a latency
anomaly; big prefix swings between snapshots signal routing churn — and it scores
each worker's **agreement** against the settled consensus, which feeds trust
(Chapter 12). Results are pushed to VPS Advisor through an **outbox** with
at-least-once, idempotent delivery, so website downtime never loses data.

> Full treatment: [Measurement, consensus & trust](../concepts/measurement-and-consensus.md).

# Chapter 12 — Trust & Security

VAPN's security model starts from three assumptions taken literally: **malicious
workers exist, credentials get compromised, measurements are unreliable.** The
community runs the workers; the platform must stay trustworthy anyway.

**Trust** is a continuous score in [0,1] per worker, its weight in consensus,
recomputed continuously from four parts: **agreement** (dominant — how well its
measurements match the *settled* consensus, so a worker that spots an outage early
isn't punished), **availability** (heartbeat regularity), **tenure** (a slow ramp
so new workers start near the floor and rise over ~2 weeks), and **penalties**
(bad signatures, replays — subtract sharply, decay slowly). Non-`active` workers
always weigh zero. The formula and worked examples are in the
[trust walkthrough](../walkthroughs/trust-calculation.md).

**Reputation is a lifecycle**: `pending` → `active` → possibly `quarantined`
(shadow mode: measures at weight 0, earning trust back) → `suspended` or
`retired`. Automation may quarantine; only a human retires or reinstates —
**administrators always outrank automation.**

**Authentication** is Ed25519 request signing over HTTPS: every request carries a
timestamp (±120 s window) and a per-worker nonce, giving replay protection without
perfectly synced clocks. Keys rotate (with an overlap window) and can be revoked
instantly. Observations are individually signed, so provenance survives forever.

The **threat matrix** is explicit and each threat has a mitigation: fabricated
measurements (redundancy + trust weighting + shadow-mode quarantine); **Sybil**
attacks (manual approval + tenure ramp + per-operator weight caps); stolen keys
(rotation + revocation + anomaly-triggered re-verification); replays (timestamp +
nonce); a worker probing its own network (source-ASN exclusion); tampered
snapshots (signed manifests, downgrade protection); malicious admins (append-only
audit, attributed actions, separated tokens); coordinator DoS (per-worker rate
limits, batch-only uploads, stateless scaling); and poisoned routing data
(bogon/MOAS validation, sanity gate, rollback).

Everything security-relevant lands in an **append-only audit log**, and security
aggregates flow to the VPS Advisor admin dashboard so site admins see the posture
without platform access.

> Full treatment: [Security & trust model](../architecture/05-security-trust-model.md).

# Chapter 13 — VPS Advisor Integration

VAPN is only useful connected to VPS Advisor, and the two meet at a small, precise
contract. The website is the source of truth and human surface; the platform is
the measurement machine. **Four flows** cross the boundary:

1. **Provider catalog** — platform *pulls* which providers/ASNs to monitor
   (~2 min).
2. **Enrollment** — platform *pulls* pending worker enrollments (~2 min).
3. **Admin decisions** — platform *pulls* approve/suspend/quarantine/retire
   decisions (~2 min).
4. **Results** — platform *pushes* verdicts, anomalies, and fleet telemetry
   (~15 s / ~5 min).

The design principles that keep this robust: **every push is idempotent** (the
outbox delivers at-least-once), **pulls tolerate the full list** (cursors are
optimizations), **identities align** (the website mints the worker UUID; the
platform adopts it), and **the platform stores no provider business data** beyond
IDs, names, ASNs, and priority.

The website team implements this additively — new Django models (`monitoring_*`),
a handful of DRF endpoints, a scoped service credential, Celery housekeeping jobs,
three permissions, admin/operator/provider dashboard pages, and a public Network
Health section. Crucially, the **platform side is already built and tested against
a stub of this exact contract** (`internal/mockadvisor`), so implementing the
website side to spec makes integration a config change on the platform. Rollout is
incremental: catalog first (read-only), then results (public status live), then
enrollment/decisions (community onboarding) — with workers enrollable via the
admin CLI until the website UI ships.

> Full treatment: [Django integration guide](../integration/django-integration.md)
> and the [API reference](../api/README.md).

---

# Part III — Running It

# Chapter 14 — Deployment

The supported v1 deployment is deliberately modest: **a single Ubuntu VM running
Docker Compose behind Caddy.** Every component is a container image built from
the monorepo — `coordinator`, `aggregator`, `builder`, `worker`, `migrate` —
plus PostgreSQL, an S3-compatible artifact store, and monitoring (Prometheus +
Grafana).

Only the coordinator and artifact store are Internet-exposed; the aggregator and
builder have no inbound surface. Caddy terminates TLS and restricts the admin
surface to an allowlisted CIDR. Secrets (database DSNs per least-privilege role,
the VPS Advisor service credential, the snapshot signing key, admin tokens) live
in an env file outside the repo. The artifact store is provider-agnostic —
Backblaze B2 is the production choice, but switching to R2, S3, or MinIO is an
environment change, not a code change — and is CDN-frontable for worker downloads
at scale.

For larger scale the same images move to Kubernetes: the coordinator as an
autoscaled Deployment, the aggregator as a single leader, the builder as a
CronJob, migrations as a pre-upgrade job, PostgreSQL managed, and the artifact
store behind a CDN.

> Full treatment: [deployment guide](../operations/deployment.md) and
> [architecture 07](../architecture/07-deployment.md).

# Chapter 15 — Operations

Running VAPN day to day is a small, well-defined set of concerns:

- **Monitoring.** Structured JSON logs everywhere; Prometheus `/metrics` on every
  service; `/healthz` and `/readyz` endpoints. Key alerts: snapshot age > 2× its
  cadence, growing publication-outbox depth, a drop in active workers, a rising
  `insufficient_data` ratio, and signature-failure spikes.
- **Backups.** Nightly base backups + WAL archiving. The registry and aggregation
  data are precious; raw measurements are re-derivable and short-lived.
- **Upgrades.** Backward-compatible migrations (expand → migrate → contract) let
  coordinator replicas roll; workers tolerate brief coordinator downtime by
  queuing observations locally. Worker fleets move forward via health-gated
  self-update and min-version enforcement.
- **Incident response & recovery.** A global scheduler kill switch
  (`vapnctl scheduler pause`), instant snapshot rollback, and worker
  suspend/quarantine give operators sharp levers. Runbooks cover the common
  incidents.

The administrative CLI `vapnctl` is the operational control plane — fleet status,
worker lifecycle, snapshot rollback, scheduler kill switch, audit query — mirrored
by the VPS Advisor admin dashboard for human operators.

> Full treatment: [Operations](../operations/README.md) — deployment, monitoring,
> backup & DR, upgrades, release management, security hardening, runbooks, and the
> launch checklist.

# Chapter 16 — Development

VAPN is a Go monorepo: `cmd/` holds one main package per binary, `internal/` the
implementation packages (each with a doc comment stating its responsibility and
boundary), `migrations/` one ordered SQL stream, `deploy/` the compose/prod/worker
manifests, and `docs/` this documentation.

A single command brings up the whole loop locally — `make dev-up` starts
PostgreSQL, MinIO, a **mock VPS Advisor** serving the integration contract from
fixtures, the coordinator, the aggregator, and workers, running fully offline
against the pre-downloaded `data/`. `make check` (vet + tests + build) is the
pre-commit gate; tests run against a dedicated `vapn_test` database, never the
live one.

Contributions must preserve the load-bearing **invariants**: VPS Advisor is the
only source of truth for providers; only aggregated consensus leaves the platform;
workers are the hostile edge; the builder is the only MRT/MaxMind reader; never
guess on ambiguity; measurement is protocol-agnostic; idempotency everywhere data
crosses a boundary; everything containerized with 12-factor config. A change that
would violate one needs a design discussion first.

> Full treatment: [Development guide](../development/README.md) and the
> [Reference](../reference/README.md) pages (CLI, configuration, schema).

# Chapter 17 — Troubleshooting

Most issues fall into a few buckets, and two commands solve most worker problems:
`vapn doctor` (re-runs the environment checks with specific fixes) and `vapn logs`
(shows what the worker is actually doing).

Common worker symptoms: stuck "awaiting approval" (a human step — check the VPS
Advisor dashboard); "unreachable" (crash, offline host, or blocked egress); clock
check failing (enable NTP); Docker permission errors (add the user to the `docker`
group); coordinator unreachable (allow outbound HTTPS 443); quarantine (usually a
wrong clock or an outdated image — fix it and trust recovers).

Common platform-side concerns: snapshots not publishing (check the builder logs;
the failing stage is named, and the previous published snapshot stays in force); a
build held for approval (investigate the prefix-count swing before forcing it);
growing outbox depth (VPS Advisor is rejecting or slow — check for 4xx, which
signals contract drift).

> Full treatment: [worker troubleshooting](../getting-started/troubleshooting.md),
> the [FAQ](../getting-started/faq.md), and [operations runbooks](../operations/runbooks.md).

# Chapter 18 — Future Roadmap

VAPN v1 is intentionally correct-and-conservative, with clean seams for growth:

- **More probe protocols.** ICMP is the first `Prober`; TCP-connect, traceroute,
  and HTTP(S) checks slot in behind the same interface and the typed metrics
  schema, moving beyond pure reachability toward service-level signals.
- **Richer routing intelligence.** RPKI origin validation layered onto the
  existing bogon/MOAS checks; cross-collector agreement for stronger ownership;
  BGP-hijack-aware interpretation of measurements.
- **Regional depth.** Finer region bucketing and per-region confidence as the
  worker fleet grows denser.
- **Scale-out seams already present.** A Redis nonce cache for the coordinator,
  CDN-fronted artifact distribution, and aggregation parallelized by provider —
  all noted as explicit future seams, none a v1 dependency.
- **Operational maturity.** Deeper anomaly detection, historical analytics, and
  tighter VPS Advisor dashboard integration.

The north star, from the brief: VAPN should read like a **mature, production-ready
open-source distributed Internet-observability platform** — one that could power
VPS Advisor for many years and eventually serve tens of thousands of providers and
thousands of community workers worldwide.

---

## Appendix — Where to go next

- **Every term:** [Glossary](../reference/glossary.md)
- **Every command:** [CLI reference](../reference/cli.md)
- **Every setting:** [Configuration](../reference/configuration.md)
- **Every endpoint:** [API reference](../api/README.md)
- **Every design decision:** [Architecture](../architecture/README.md)
- **The friendly on-ramp:** [Getting Started](../getting-started/README.md)

## Exporting this handbook as a PDF

This handbook is a single self-contained Markdown file, which makes PDF export
straightforward. For example, with [Pandoc](https://pandoc.org):

```sh
pandoc docs/handbook/README.md -o vapn-handbook.pdf \
  --toc --toc-depth=2 --number-sections \
  -V geometry:margin=1in -V documentclass=report
```

Or open it in any Markdown editor with PDF export (VS Code + a Markdown PDF
extension, Typora, Obsidian). The Mermaid diagrams render in tools with Mermaid
support; for plain LaTeX PDF export, pre-render diagrams or rely on the prose,
which is written to stand on its own.
