# 04 — API Contracts

Three API surfaces. Full request/response schemas with examples and error catalogs are a
Phase 9/11 deliverable (`docs/api/`); this document fixes the contracts: who calls what,
auth, endpoint inventory, and representative payloads.

Conventions (all surfaces): JSON over HTTPS; `application/json`; RFC 7807 problem+json
errors; cursor pagination (`?cursor=`&`limit=`); idempotency via client-supplied
`Idempotency-Key` on mutating platform calls; versioned base paths (`/api/v1/`).

---

## A. VPS Advisor endpoints (to be implemented by the website team)

Consumed by this platform (server-to-server) or by humans. Platform authenticates with a
static service credential + HMAC (or mTLS if the site supports it); admin/dashboard
endpoints use the site's existing session auth + new permissions.

### A1. Provider APIs (platform pulls)

| Method & path | Purpose |
|---|---|
| `GET /api/v1/monitoring/providers` | List providers eligible for monitoring. Filter: `?enabled=true`, `?updated_since=`. Returns provider_id, name, asns[], monitoring_enabled, priority, metadata. |
| `GET /api/v1/monitoring/providers/{id}` | Single provider detail. |
| `GET /api/v1/monitoring/asns` | Flat ASN→provider mapping (cheap delta sync; `?updated_since=`). |

```json
// GET /api/v1/monitoring/providers?enabled=true  (200)
{ "providers": [ { "provider_id": "7f9c…", "name": "ExampleHost",
    "asns": [64500, 64501], "monitoring_enabled": true, "priority": 10,
    "updated_at": "2026-07-18T06:00:00Z" } ], "next_cursor": null }
```

### A2. Worker enrollment & operator APIs (human + platform)

| Method & path | Purpose |
|---|---|
| `POST /api/v1/monitoring/workers` | Operator creates a worker record; returns one-time enrollment token (shown once). |
| `GET /api/v1/monitoring/workers` | Operator lists own workers + status. |
| `POST /api/v1/monitoring/workers/{id}/regenerate-token` | Replace unused/expired token. |
| `GET /api/v1/monitoring/enrollments/pending` | Platform pulls pending enrollments + token hashes + operator IDs. |
| `POST /api/v1/monitoring/enrollments/{id}/registered` | Platform reports key registration (site shows "awaiting approval"). |

### A3. Administration APIs (admin UI + platform sync)

| Method & path | Purpose |
|---|---|
| `POST /api/v1/monitoring/admin/workers/{id}/approve` \| `suspend` \| `quarantine` \| `retire` | State transitions with reason; site records decision, platform pulls (or receives webhook). |
| `GET /api/v1/monitoring/admin/decisions?since=` | Platform syncs admin decisions (poll fallback for webhooks). |
| `POST /api/v1/monitoring/admin/workers/{id}/rotate-key` | Order credential rotation. |

### A4. Results API (platform pushes)

| Method & path | Purpose |
|---|---|
| `PUT /api/v1/monitoring/results/providers/{provider_id}` | Upsert aggregated status document (global + per-region verdicts, metrics, confidence). |
| `POST /api/v1/monitoring/results/anomalies` | Open/update/resolve anomaly events (drives "recent routing instability" UI). |
| `POST /api/v1/monitoring/results/history` | Periodic rollup batches for historical charts. |
| `POST /api/v1/monitoring/telemetry/fleet` | Fleet summary for admin dashboard (worker counts by state/version/region, snapshot version in force, security event counts). |

```json
// PUT …/results/providers/7f9c…  (body)
{ "as_of": "2026-07-18T08:05:00Z", "global": { "verdict": "healthy",
    "confidence": 0.97, "rtt_p50_ms": 21.4, "loss_rate": 0.001 },
  "regions": [ { "region": "eu-west", "verdict": "healthy", "confidence": 0.99 },
               { "region": "ap-south", "verdict": "insufficient_data", "confidence": 0 } ],
  "active_anomalies": [] }
```

### Required website changes (summary for the integration guide)

New DB models (worker, enrollment token, decision log, result documents), the endpoints
above, a service-credential auth mechanism for the platform, operator dashboard pages
(my workers, enrollment flow), admin pages (approval queue, fleet health, trust view,
audit log), permissions (`monitoring.operator`, `monitoring.admin`), scheduled cleanup
of stale enrollments, and notification events (worker approved/suspended, provider
anomaly opened). Detailed in the Phase 9 integration guide.

---

## B. Coordinator API (this platform; workers call it)

Auth: Ed25519 request signing (see [05](05-security-trust-model.md)) — headers
`X-Worker-Id`, `X-Timestamp`, `X-Nonce`, `X-Signature` over method|path|timestamp|nonce|
body-sha256. Exception: `POST /register` authenticates by enrollment token.

| Method & path | Purpose |
|---|---|
| `POST /api/v1/workers/register` | One-time: enrollment token + public key + worker facts → worker_id, state `pending`. |
| `POST /api/v1/workers/heartbeat` | Liveness + version + resource stats → returns config, lease renewals, snapshot version advertisements, pending control actions (rotate key, drain, suspend). |
| `GET /api/v1/artifacts/routing/current` | Snapshot manifest (version, url, sha256, signature, min_worker_version). |
| `POST /api/v1/assignments/lease` | Request work: capacity + capabilities → assignment batch with lease expiry. |
| `POST /api/v1/assignments/release` | Voluntarily return assignments (shutdown/drain). |
| `POST /api/v1/observations` | Upload signed observation batch (idempotent by batch id). 207-style per-item accept/reject. |
| `POST /api/v1/workers/keys/rotate` | Submit next public key, signed by current key; response confirms overlap window. |
| `GET /api/v1/workers/me` | Own state/trust snapshot (transparency to operators). |

```json
// POST /api/v1/observations  (body, abridged)
{ "batch_id": "0197a3…", "observations": [ { "assignment_id": 812,
    "target": "203.0.113.7", "probe_type": "icmp",
    "measured_at": "2026-07-18T08:04:31.201Z", "ok": true,
    "rtt_ms": 22.9, "packets_sent": 4, "packets_lost": 0,
    "signature": "base64…" } ] }
```

Status codes: 401 invalid signature/expired ts, 403 wrong state (suspended worker), 409
replayed nonce/batch, 422 malformed observation, 429 policy rate-limit.

## C. Platform admin/internal API (coordinator)

Used by platform operators and the (future) admin CLI; authenticated by admin tokens,
network-restricted. Endpoints: worker CRUD/state overrides, snapshot promote/rollback,
assignment inspection, policy editing, audit query. Mirrors what the VPS Advisor admin
dashboard drives via A3 sync — the site is the primary UI; this surface is the
operational escape hatch and automation hook.
