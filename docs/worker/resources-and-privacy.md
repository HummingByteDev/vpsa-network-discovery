# Worker Resource Usage & Privacy

The two questions every prospective worker operator asks: **"What will it cost
me?"** and **"What can it see?"** This page answers both plainly. If anything
here doesn't match what you observe, that's a bug worth reporting.

## Resource usage

A worker is intentionally tiny. It's a single static Go binary in a minimal
container that spends almost all its time asleep between probes.

| Resource | Typical usage | Notes |
|---|---|---|
| **Memory** | a few MB | No routing files to hold — the worker carries only a small signed SQLite target list |
| **CPU** | negligible | Sending pings and signing small batches; idle otherwise |
| **Bandwidth** | a trickle | A few ICMP packets per minute per assigned target, plus small signed uploads and a ~30 s heartbeat |
| **Disk** | tens of MB | The worker image, the current snapshot, and a bounded local upload queue |
| **Network ports** | **none inbound** | The worker only makes *outbound* HTTPS (443) connections; it opens no listening ports |

### Why it's so light

- **Workers never parse routing data.** The heavy lifting (parsing ~1M MRT
  records) happens once in the [builder](../builder/README.md); workers get a
  pre-digested target list. → [why workers never touch MRT](../concepts/ripe-and-ris.md#why-workers-never-touch-mrt)
- **Probes are small and paced.** ICMP echo is a handful of tiny packets, and
  probe intervals are set by policy — the platform actively prevents anything
  that would resemble a scan or abuse.
- **Uploads are batched.** Observations are collected and sent roughly once a
  minute, signed, in one request — not one request per probe.

### Controlling the load

- `vapn pause` stops all probing instantly (and returns your assignments to the
  fleet) — use it when you need your bandwidth.
- The platform caps assignments per worker
  ([`VAPN_MAX_ASSIGNMENTS_PER_WORKER`](../reference/configuration.md#coordinator)) and
  enforces per-target and per-worker rate limits via **probe policy**, so a
  worker can't be handed an abusive amount of work.

## Privacy

This is the part people care about most, so here it is directly.

### What the worker does

- **Probes only platform-provided targets.** It sends ICMP echo ("ping")
  packets **only** to addresses in the signed routing snapshot, which is derived
  exclusively from [monitored providers' publicly announced routes](../concepts/prefix-ownership.md).
  It **never chooses its own targets** and never scans.
- **Reports only its own measurements and liveness.** It uploads the probe
  results (reachability, RTT), its software version, and a heartbeat. That's the
  entire outbound story.

### What the worker does *not* do

- It does **not** read your files, your other network traffic, your processes,
  or anything else on your machine. It runs as an unprivileged container with a
  single Linux capability, `CAP_NET_RAW` (needed to send ICMP), and a single
  small volume for its own state.
- It does **not** use host networking or open inbound ports.
- It does **not** phone home about your machine's identity beyond what's
  inherent in making an outbound connection (your public IP, which the platform
  geolocates to a region and source network — see below).

### Your key stays yours

The worker generates an **Ed25519** keypair on first boot and the **private key
never leaves your host** — not in logs, not to the coordinator, never. This is a
deliberate asymmetry: the platform can *verify* your measurements are yours, but
it **cannot impersonate you or forge measurements in your name**. If your key
were ever stolen, rotation and revocation limit the damage — see
[worker authentication](../walkthroughs/worker-authentication.md).

### What the platform learns about you

Only what's necessary to keep measurements fair and honest:

- **Your public IP's region and source ASN** (via [GeoIP](../concepts/geoip.md)),
  used to (a) spread each target's measurement across diverse networks and
  (b) **stop you from measuring your own provider's network**. It's a coarse
  network/region signal, not tracking.
- **Your operator account** (on VPS Advisor) — because a human vouches for each
  worker. Per-operator weight caps mean one person can't dominate consensus.
- **Your worker's behavior** — trust events like invalid signatures or a wrong
  clock, used to score [trust](../concepts/measurement-and-consensus.md#trust).

### Data handling

Raw measurements are **internal-only** and retained briefly (default ~14 days)
before being pruned; only aggregated, consensus-backed verdicts are ever
published, and those are about *providers*, not about you. See
[data retention](../architecture/03-database-design.md) and the
[security model](../architecture/05-security-trust-model.md).

## The honest summary

Running a worker costs you a few MB of RAM and a trickle of outbound bandwidth,
opens no ports, and lets a sandboxed container send pings to a fixed, signed
list of provider addresses — nothing more. Your private key and your machine's
contents stay yours. You can pause or remove it at any moment. If that trade
sounds fair, [run one](installation.md) — and thank you.
