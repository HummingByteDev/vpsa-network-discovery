# Security: Hardening, Threat Verification & Compromise Response

Threat model recap (design record:
[architecture/05 — Security & trust model](../architecture/05-security-trust-model.md)):
malicious workers, stolen worker credentials, and unreliable measurements are
*assumed* and handled in-protocol — signatures, replay protection, consensus,
trust weighting, quarantine. This document covers what the protocol cannot
handle (the platform host and the platform secrets), verifies each modelled
threat against the implementation, and gives the response procedure for each
compromise scenario.

## Stack hardening (shipped by default in `deploy/prod`)

- Distroless, CGO-free images; `read_only: true` root filesystems for
  coordinator and aggregator; no shell in production containers.
- Single exposed service (Caddy, 80/443). `/metrics`, `/healthz`, postgres and
  the aggregator are reachable only on the internal Docker network.
- `/admin/v1` is CIDR-allowlisted at the edge (`VAPN_ADMIN_ALLOW_CIDR`) **and**
  bearer-token authenticated at the coordinator (`VAPN_ADMIN_TOKEN`).
- Secrets only in `.env` (chmod 600) — never in images or the repository.
- Log rotation bounded (json-file, 20 MB × 5 per service).
- The dev-only `VAPN_DEV_ENROLLMENT_TOKEN` is absent from the production
  compose file — enrollment always requires per-worker one-time tokens. The
  coordinator logs a loud warning if it is ever set.

## Host checklist (Ubuntu LTS)

- `ufw default deny incoming; ufw allow 22,80,443/tcp; ufw enable`
  (Docker publishes only through Caddy, but belt-and-braces).
- SSH: keys only, no root login; `unattended-upgrades` for the OS.
- Time sync on (`timedatectl`) — request signing tolerates ±2 min skew; a
  drifting platform clock would lock the whole fleet out.
- Disk headroom alerting: the builder needs roughly 10 GB free per run.
- The VM is single-purpose: nothing else runs on it.

## Credential inventory & rotation

| Credential | Held by | Rotate |
|---|---|---|
| `VAPN_ADMIN_TOKEN` | operators, `vapnctl` | edit `.env`, `docker compose up -d coordinator`; update operator shells |
| `VAPN_ADVISOR_TOKEN` | platform ↔ website | issue a second token website-side, swap, revoke the old one |
| S3 key pair | builder, coordinator | issue a new key at the provider, swap, revoke |
| DB password | in-stack services | edit `.env`, `docker compose up -d` (postgres + services) |
| Snapshot signing key | builder only | see below — expensive, plan it |
| Worker keys | each worker | self-rotating; an admin can demand rotation with `vapnctl workers rotate-key <id>` |

**Signing-key rotation** is the expensive one, because every worker pins the
public half. Procedure: generate a new pair
([Step 3 of the builder installation](../builder/installation.md#step-3--generate-the-snapshot-signing-key))
→ publish the new **public** key to worker operators → switch the builder's
private key in `.env` → the next snapshot is signed with the new key. Workers
that haven't been given the new public key keep their last snapshot and refuse
new ones, so plan a deprecation window and announce it.

---

## Threat matrix verification

A pre-launch pass tracing every threat in
[architecture/05](../architecture/05-security-trust-model.md) to its implemented
mitigation and the evidence that the mitigation works. **Re-run this review
whenever the worker protocol or the trust model changes.**

| # | Threat | Mitigation (where) | Evidence |
|---|---|---|---|
| T1 | Forged worker requests | Ed25519 request signing over method\|path\|timestamp\|nonce\|body-hash (`internal/wireauth`) | `wireauth` unit tests; unsigned/mis-signed → 401 (`security_test.go`) |
| T2 | Replayed requests | Nonce table + ±2 min skew window; replay → 409 + trust event (`coordinator.signed`) | `TestReplayRejected`; `vapn_trust_events_total{type="replay"}` |
| T3 | Stolen worker credential | Per-worker keys; suspension locks out at the next signed call; admin-demanded rotation via heartbeat | `TestSuspensionLockout`, `TestDemandedRotation` |
| T4 | Fabricated measurements | Per-observation signatures verified at intake against registered keys; observations bind to held assignments | `measurement_test`: wrong key / unheld assignment rejected |
| T5 | Lying workers (plausible fakes) | Trust-weighted consensus; agreement feedback demotes dissenters; weight floor keeps them observable | `TestLiarDemoted`; trust formula in `internal/trust` |
| T6 | Worker measuring its own provider | Self-ASN exclusion in the lease claim SQL (GeoLite2-ASN source resolution) | `TestSchedulerSimulation` asserts zero self-ASN leases |
| T7 | One worker monopolizing a target's replicas | Redundancy-group uniqueness per worker, enforced across *and within* lease calls | sim test dupes check; distinct-on candidates CTE |
| T8 | Malicious/corrupted routing snapshot | Manifest signed by the builder key workers pin; hash-verified download; readback verification before publish; targets outside the snapshot refused by workers | `manifest_test` (fail-closed), `TestWorkerRefusesUnsignedSnapshot`, executor `targetInSnapshot` |
| T9 | Compromised artifact store | Same as T8 — the store is untrusted by design; a bad object cannot become a worker target list without a valid signature | readback + pin tests above |
| T10 | Snapshot data poisoning via a RIS glitch | Sanity gate (swing beyond `VAPN_SANITY_MAX_DELTA_PCT` → exit 2, held in `building`) | live-verified twice (phase 10/11 demos); `builder_test` |
| T11 | Enrollment abuse / worker floods | One-time hashed tokens (24 h expiry), manual approval, pending workers get no work and no weight | `TestAdvisorSyncFlow`; runbook [worker-flood](runbooks.md#worker-flood-no-alert-admin-observed) |
| T12 | Admin token abuse | Bearer token + edge CIDR allowlist; every admin action audit-logged; powers limited to lifecycle/rollback (no key material) | `TestAdminSurface` 401 test; `audit.event` rows |
| T13 | VPS Advisor credential abuse | Scoped to the monitoring namespace; catalog conflicts (an ASN claimed twice) hard-error instead of guessing; decisions applied idempotently | `advisor.SyncProviders` conflict test; integration guide §2 |
| T14 | Platform host compromise | Out of protocol scope — hardening + DR + full credential rotation (below) | [compromise response](#compromise-response) |
| T15 | DoS via the data plane | Body limits (1 MB), lease capacity clamp, batch idempotency (duplicate upload batches acked, not re-inserted), bounded worker queues | `measurement.go` limits; `upload_batch` table; load test (500 workers, 0 errors) |
| T16 | Worker → provider abuse (outbound) | ICMP echo only, rates fixed by assignment intervals, targets only from the signed snapshot, provider opt-out drains within ~2 min | probe engine; opt-out drain in `TestAdvisorSyncFlow`/scheduler |

### Residual risks (accepted, documented)

- **Signing-key custody** is the single most valuable secret; mitigations are
  procedural (secret store, rotation plan) — see above.
- **Sybil operators** (many fake identities producing honest-looking
  measurements) are rate-limited by manual approval and diversity-aware
  confidence, not eliminated; regional confidence requires geographic diversity
  by design.
- **BGP-level spoofing of probe responses** is out of scope: verdicts are about
  reachability consensus, not path authenticity.

---

## Compromise response

### A worker is compromised or malicious

The protocol already contains it (trust weighting + consensus). Operationally:

```sh
vapnctl workers suspend <id> --reason "..."      # hard lockout at next signed request
vapnctl workers quarantine <id> --reason "..."   # keep observing at zero public weight
```

Evidence: `vapnctl workers show <id>`, `vapnctl audit`, `registry.trust_event`.
Aggregation is *not* poisoned retroactively — if a long-trusted worker turns out
bad, its windows can be recomputed, because measurements are per-worker
attributed and signed.

### The admin token leaks

Rotate immediately (table above). Review
`vapnctl audit --category admin --since <suspected time>` — every admin action
is audit-logged. Worst-case admin powers are worker state changes, scheduler
pause, and snapshot rollback: disruptive but recoverable. The token cannot
exfiltrate key material or forge measurements.

### The advisor credential leaks

It can read the monitoring catalog and enrollments and push fake statuses to the
website. Rotate website-side, then in the platform `.env`. Audit the website's
`monitoring_provider_status` for suspect writes.

### The signing key leaks (without host compromise)

An attacker with the key **and** write access to the artifact store could feed
workers a malicious target list, pointing the fleet at a victim. Workers cap the
damage: ICMP-only probes, rate-limited by assignment intervals, and every target
must be inside the snapshot.

Response: rotate the signing key (procedure above), audit the store's access
logs, rotate the S3 credentials, and consider a `vapnctl scheduler pause` while
you assess.

### The platform host is compromised

Treat everything on it as burned:

1. Rebuild from the repository and secret store on a fresh VM — the
   [DR procedure](backup-restore.md#disaster-recovery-full-vm-loss).
2. Rotate **all** credentials in the inventory above, **including the signing
   key**.
3. Audit the `registry` and `audit` schemas from the last clean backup for
   planted workers or unexplained state changes.

Workers are unaffected infrastructure-wise — their private keys never touch the
platform — but treat snapshots published and verdicts produced during the
compromise window as untrusted: roll back and recompute.
