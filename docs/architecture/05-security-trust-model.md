# 05 — Security & Trust Model

Design assumptions (from the brief, taken literally): **malicious workers exist,
compromised credentials exist, unreliable measurements exist.** The community runs the
workers; the platform must stay trustworthy anyway.

## 1. Identities & credentials

| Principal | Credential | Notes |
|---|---|---|
| Worker | Ed25519 keypair, generated on the worker at first boot; private key never leaves the container volume | Public key registered during enrollment via one-time token |
| Platform → VPS Advisor | Service credential (bearer + HMAC or mTLS) | Site team's choice; scoped to monitoring endpoints only |
| VPS Advisor → platform (webhooks, optional) | Shared-secret HMAC signature | Poll fallback exists, so webhooks are an optimization |
| Snapshot artifacts | Platform signing key (Ed25519), offline-storable | Workers pin the public key baked into the image + rotatable via signed config |
| Admins | VPS Advisor session + permission (`monitoring.admin`); platform admin API uses separate scoped tokens | Admin actions always audited |

## 2. Worker request authentication

Every worker request (except registration) carries:

```
X-Worker-Id: <uuid>
X-Timestamp: <RFC3339, ±120s server-clock tolerance>
X-Nonce:     <128-bit random>
X-Signature: Ed25519( method | path | timestamp | nonce | sha256(body) )
```

- **Replay protection:** timestamp window + per-worker nonce uniqueness within the
  window (`registry.replay_nonce`, TTL-pruned). Replays → 409 + TrustEvent.
- **Key rotation:** worker submits its *next* public key signed by the current one;
  both valid during a bounded overlap; server can *demand* rotation via heartbeat
  control action (used on suspected compromise and on a routine schedule).
- **Revocation:** revoking all keys instantly cuts a worker off; state transitions to
  `suspended`/`quarantined` decide what happens next.
- Observations are *individually* signed too, so a batch relayed through any future
  ingestion path retains per-measurement provenance.

## 3. Threat matrix (v1 scope)

| Threat | Mitigation |
|---|---|
| Fabricated measurements (worker lies) | Redundancy (N workers per target, disjoint operators/ASNs where possible), trust-weighted consensus, dissent scoring, shadow-mode quarantine |
| Sybil workers (one actor, many nodes) | Operator-level identity on VPS Advisor; approval is manual; redundancy groups spread across *operators and source ASNs*, not just workers; per-operator caps on consensus weight |
| Stolen worker key | Rotation + revocation; anomaly signals (source IP/ASN change triggers re-verification and weight drop until re-approved) |
| Replay of old (healthy) observations | Timestamp/nonce; observations bind assignment_id + measured_at; server rejects stale measured_at outside window |
| Worker probing its own provider's network | Worker's source ASN recorded; scheduler excludes assignments where target ASN == worker source ASN (or same operator's claimed provider) |
| Malicious targets / probe abuse | Workers only probe targets from signed snapshots derived from monitored ASNs; ProbePolicy rate caps per target and per worker; global kill-switch via heartbeat config |
| Tampered snapshot artifact | Signed manifest; workers verify sha256 + signature against pinned key before swap; downgrade protection via monotonic version check |
| Compromised artifact store | Same as above — store is untrusted by design |
| Malicious platform admin (insider) | Append-only audit schema, admin actions attributed, separation of platform-admin vs site-admin tokens |
| DoS of coordinator by workers | Per-worker rate limits, batch-only uploads, 429 + suspension for abuse; coordinator stateless/scalable |
| Bad RIS data (route leaks poisoning targets) | Bogon/MOAS validation in builder, diff alarms on wild prefix-count swings, publish gate (human-approvable threshold) |

Out of scope for v1 (documented, revisited later): TLS interception of worker egress
(mitigated by signing anyway), physical worker compromise beyond key theft, BGP
hijack-aware measurement interpretation.

## 4. Trust model

Trust is a continuous score in [0,1] per worker, recomputed by the aggregator each
scoring window, from:

- **Consensus agreement** (dominant term): agreement of the worker's observations with
  the trust-weighted consensus of its redundancy groups. Disagreement when the worker is
  *right* early in an outage is protected by scoring against the *final* settled window,
  not the instantaneous majority.
- **Availability**: heartbeat regularity, lease fulfillment ratio.
- **Tenure**: slow logistic ramp — new workers start near the floor; weight grows over
  weeks, capping Sybil value.
- **Penalties**: TrustEvents (bad signatures, replays, policy violations) subtract
  sharply and decay slowly.

Weight in consensus = f(trust) with a hard floor of 0 for non-`active` workers and a cap
per operator. Consensus itself: per window, per (provider, region, probe type) — robust
aggregation (trimmed weighted median for RTT, weighted vote for reachability), verdict
requires minimum worker count + operator diversity, otherwise `insufficient_data`
(never guess).

**Lifecycle levers (admin-controlled, from the brief):** approval (pending→active),
suspension (no assignments, no auth), quarantine (shadow mode: measures, weight 0,
earning trust back), retirement (permanent, keys revoked, history retained), credential
rotation (forced). Administrators always outrank automation — automatic transitions may
*suggest* (flag for review) or *quarantine*, but only admins retire or reinstate.

## 5. Audit & security telemetry

Every auth failure, state transition, admin action, snapshot publication, and policy
change lands in `audit.event` (append-only). Security-relevant aggregates (failed
signature counts, replay attempts, per-worker rejection rates) flow to the VPS Advisor
admin dashboard via the fleet telemetry push, so site admins see security posture
without platform access.
