# Phase 6 Demo — Authentication & Trust

What exists: the full worker-auth hardening pass — replay protection is now
enforced (not just designed), key rotation works both voluntarily and on admin
demand with a bounded overlap window, security failures generate trust events,
a first trust score is computed continuously, and security/admin actions land
in the append-only audit log.

## Replay protection

Every signed request's nonce is recorded (`registry.replay_nonce`); a
byte-identical replay gets **409** and a `replay` trust event. Nonces are
pruned after 2× the ±2 min timestamp window (coordinator maintenance loop), so
the table stays tiny while covering every timestamp still accepted.

```sh
docker exec vapn-dev-postgres-1 psql -U vapn -d vapn -c \
  "select event_type, count(*) from registry.trust_event group by 1;"
```

## Key rotation

- **Voluntary:** `POST /api/v1/workers/keys/rotate` with the next public key,
  signed with the *current* key (proof of possession). The old key stays valid
  for a 10-minute overlap, then ages out; verification accepts any
  currently-valid key for the worker, so in-flight requests never break.
- **Admin-demanded:** `POST /admin/v1/workers/{id}/rotate-key` flags the
  worker; its next heartbeat carries the `rotate_key` action and the agent
  rotates autonomously — generate, register (signed with old), persist, swap.
  A failed registration leaves the worker on its old, still-valid key.

```sh
AUTH='Authorization: Bearer dev-admin-token'
curl -s -H "$AUTH" -X POST localhost:8080/admin/v1/workers/<id>/rotate-key -i
docker exec vapn-dev-postgres-1 psql -U vapn -d vapn -c \
  "select worker_id, valid_from, valid_until, revoked_at from registry.worker_key order by id;"
```

## Trust score (skeleton)

The aggregator recomputes per-worker trust every minute
(`VAPN_TRUST_INTERVAL`):

```
score = clamp( availability × (0.3 + 0.7 × tenure) − penalty , 0, 1 )
```

- availability: heartbeat recency (1.0 within 5 min, 0.5 within 1 h, else 0)
- tenure: `d/(d+14)` over days since approval — Sybil-capping slow ramp
- penalty: 0.1 per bad-signature/replay event in 7 days, capped at 0.5

The dominant **consensus-agreement** component joins in Phase 8 when settled
aggregation windows exist to score against. Non-`active` workers always weigh
0 in consensus regardless of score (invariant from the architecture).

```sh
docker exec vapn-dev-postgres-1 psql -U vapn -d vapn -c \
  "select worker_id, round(score::numeric,3) score, components from registry.trust_score;"
```

## Audit log

Registrations, admin state changes, rotation requests, and key rotations are
recorded in `audit.event` (append-only; audit write failures are loud in logs
but never take the platform down):

```sh
docker exec vapn-dev-postgres-1 psql -U vapn -d vapn -c \
  "select category, actor, action, subject, created_at from audit.event order by id desc limit 10;"
```

## Security test suite

`internal/coordinator/security_test.go` + `wireauth` tests, all fail-closed:
byte-identical replay → 409 + trust event; expired timestamp, tampered body,
cross-path replay, wrong key → 401; suspension locks out within one
heartbeat; rotation overlap honored and expired old keys rejected;
admin-demanded rotation completes autonomously and clears the demand; trust
component math verified against seeded fixtures.
