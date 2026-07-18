# Phase 4 Demo — Worker Framework

What exists: the community worker agent (keypair generation, enrollment,
heartbeat loop, verified snapshot sync, `doctor`), the coordinator's
worker-facing API (registration, heartbeat, artifact advertisement/download)
with Ed25519 request signing and lifecycle enforcement, and the platform admin
surface (create/approve/suspend/quarantine/retire workers). Probing is Phase 5;
replay-nonce tracking and trust scoring are Phase 6.

## 1. Bring up the fleet

```sh
make build && ./bin/keygen          # once; export both variables
export CNIP_SNAPSHOT_SIGNING_KEY=… CNIP_SNAPSHOT_PUBLIC_KEY=…
make dev-up                          # stack + 3 worker replicas
```

Workers auto-enroll with the shared dev token (`CNIP_DEV_ENROLLMENT_TOKEN`,
coordinator-side; **dev only** — production workers enroll via one-time tokens
from VPS Advisor). Within one heartbeat (5 s in dev) each worker downloads the
current artifact through the coordinator, verifies the manifest signature
against the pinned `CNIP_SNAPSHOT_PUBLIC_KEY` plus the file hash, and installs
it atomically:

```sh
docker logs cnip-dev-worker-1 | tail   # → "registered", "snapshot installed"
```

## 2. Admin surface

```sh
AUTH='Authorization: Bearer dev-admin-token'
curl -s -H "$AUTH" localhost:8080/admin/v1/workers | jq        # fleet view

# production-style enrollment: create a worker, get its one-time token
curl -s -H "$AUTH" -X POST localhost:8080/admin/v1/workers -d '{"name":"my-node"}' | jq

# lifecycle transitions (pending→active→suspended→…, enforced server-side)
curl -s -H "$AUTH" -X POST localhost:8080/admin/v1/workers/<id>/state \
  -d '{"state":"suspended","reason":"maintenance"}' -i
```

A suspended worker is locked out (403) at its next request; its heartbeats
stop and the fleet view shows the state and reason. Every transition lands in
`registry.trust_event`.

## 3. Worker self-diagnosis

```sh
docker exec cnip-dev-worker-1 /app doctor
# ✓ identity key            ok
# ✓ registration            ok
# ✓ coordinator reachable   ok
# ✓ routing snapshot        ok
```

## 4. Request signing

Every worker request (after registration) carries
`X-Worker-Id / X-Timestamp / X-Nonce / X-Signature` — Ed25519 over
`method|path|timestamp|nonce|sha256(body)` (`internal/wireauth`, shared by
both sides). The coordinator verifies against the key registered at
enrollment; ±2 min timestamp window. Unsigned or wrongly signed requests get
401 (see `TestBadSignatureRejected`).

## 5. Tests

`CNIP_TEST_DB_DSN=… go test ./internal/coordinator/ ./internal/wireauth/`:
production enrollment flow (pending workers heartbeat but receive no
snapshot; approval unlocks sync), dev auto-enrollment, suspension lockout
within one heartbeat, wrong-key rejection, plus wireauth unit tests (tampered
body / path replay / stale timestamp all fail closed).
