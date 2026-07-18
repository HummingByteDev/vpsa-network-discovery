# Security Review — Threat Matrix Verification

Pre-launch pass tracing every threat in
[architecture/05-security-trust-model.md](../architecture/05-security-trust-model.md)
to its implemented mitigation and the evidence that the mitigation works.
Re-run this review whenever the worker protocol or trust model changes.

| # | Threat | Mitigation (where) | Evidence |
|---|---|---|---|
| T1 | Forged worker requests | Ed25519 request signing over method\|path\|timestamp\|nonce\|body-hash (`internal/wireauth`) | `wireauth` unit tests; unsigned/mis-signed → 401 (`security_test.go`) |
| T2 | Replayed requests | Nonce table + ±2 min skew window; replay → 409 + trust event (`coordinator.signed`) | `TestReplayRejected`; `vapn_trust_events_total{type="replay"}` |
| T3 | Stolen worker credential | Per-worker keys; suspension locks out at next signed call; admin-demanded rotation via heartbeat | `TestSuspensionLockout`, `TestDemandedRotation` (security_test) |
| T4 | Fabricated measurements | Per-observation signatures verified at intake against registered keys; observations bind to held assignments | `measurement_test`: wrong key / unheld assignment rejected |
| T5 | Lying workers (plausible fakes) | Trust-weighted consensus; agreement feedback demotes dissenters; weight floor keeps them observable | `TestLiarDemoted` (aggregate); trust formula in `internal/trust` |
| T6 | Worker measuring its own provider | Self-ASN exclusion in the lease claim SQL (GeoLite2-ASN source resolution) | `TestSchedulerSimulation` asserts zero self-ASN leases |
| T7 | One worker monopolizing a target's replicas | Redundancy-group uniqueness per worker, enforced across *and within* lease calls | sim test dupes check; distinct-on candidates CTE |
| T8 | Malicious/corrupted routing snapshot | Manifest signed by builder key workers pin; hash-verified download; readback verification before publish; targets outside snapshot refused by workers | `manifest_test` (fail-closed), `TestWorkerRefusesUnsignedSnapshot`, executor `targetInSnapshot` |
| T9 | Compromised artifact store | Same as T8 — the store is untrusted by design; a bad object cannot become a worker target list without a valid signature | readback + pin tests above |
| T10 | Snapshot data poisoning via RIS glitch | Sanity gate (>50% swing → exit 2, held in `building`) | live-verified twice (phase 10/11 demos); `builder_test` |
| T11 | Enrollment abuse / worker floods | One-time hashed tokens (24 h expiry), manual approval, pending workers get no work and no weight | `TestAdvisorSyncFlow`; runbook `worker-flood` |
| T12 | Admin token abuse | Bearer + edge CIDR allowlist; every admin action audit-logged; powers limited to lifecycle/rollback (no key material) | `TestAdminSurface` 401 test; `audit.event` rows |
| T13 | Advisor credential abuse | Scoped to monitoring namespace; catalog conflicts (ASN claimed twice) hard-error instead of guessing; decisions applied idempotently | `advisor.SyncProviders` conflict test; integration guide §2 |
| T14 | Platform host compromise | Out of protocol scope — hardening + DR + full credential rotation procedure | security-hardening.md compromise-response |
| T15 | DoS via data plane | Body limits (1 MB), lease capacity clamp, batch idempotency (duplicate upload batches acked, not re-inserted), bounded worker queues | `measurement.go` limits; `upload_batch` table; load test (500 workers, 0 errors) |
| T16 | Worker → provider abuse (outbound) | ICMP echo only, rates fixed by assignment intervals, targets only from the signed snapshot, provider opt-out drains within ~2 min | probe engine; opt-out drain in `TestAdvisorSyncFlow`/scheduler |

## Residual risks (accepted, documented)

- **Signing-key custody** is the single most valuable secret; mitigations
  are procedural (secret store, rotation plan) — see security-hardening.md.
- **Sybil operators** (many fake identities, honest-looking measurements)
  are rate-limited by manual approval and diversity-aware confidence, not
  eliminated; regional confidence requires geographic diversity by design.
- **BGP-level spoofing of probe responses** is out of scope: verdicts are
  about reachability consensus, not path authenticity.
