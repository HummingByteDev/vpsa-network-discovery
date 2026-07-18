# Architecture — Community Network Intelligence Platform

This directory contains the Phase 1 architectural analysis required by the project brief
(`CLAUDE.md`). **No implementation exists yet**; per the implementation strategy, coding
begins only after this architecture is approved.

## Documents

| # | Document | Covers (brief item) |
|---|----------|---------------------|
| 01 | [System Architecture](01-system-architecture.md) | System architecture, component boundaries, service responsibilities (1–3) |
| 02 | [Domain Model](02-domain-model.md) | Domain model (4) |
| 03 | [Database Design](03-database-design.md) | PostgreSQL schema design (5) |
| 04 | [API Contracts](04-api-contracts.md) | API contracts, incl. required VPS Advisor endpoints (6) |
| 05 | [Security & Trust Model](05-security-trust-model.md) | Security model, trust model (7–8) |
| 06 | [Lifecycles](06-lifecycles.md) | Worker lifecycle, snapshot lifecycle (9–10) |
| 07 | [Deployment Architecture](07-deployment.md) | Deployment architecture (11) |
| 08 | [Risk Assessment](08-risk-assessment.md) | Risk assessment (12) |
| 09 | [Implementation Roadmap](09-roadmap.md) | Phased implementation roadmap (13) |

## Reading order

Read 01 first — it establishes the component names and the one deliberate clarification
made to the brief (the split between the VPS Advisor **control plane** and this platform's
**worker data plane**, see §"API plane split"). Everything else builds on it.

## Key decisions at a glance

- **Two API planes.** VPS Advisor keeps identity, provider catalog, enrollment, admin, and
  aggregated-results ingestion. This platform hosts the high-volume worker-facing
  Coordinator API (heartbeats, assignments, observation upload, snapshot download).
- **Workers never see PostgreSQL.** The Snapshot Builder maintains the canonical routing
  intelligence database in PostgreSQL and publishes a compact, signed, versioned **SQLite
  artifact** that workers download.
- **Go** for all platform services and the worker (single static binary, tiny community
  Docker image); PostgreSQL 16+ everywhere server-side.
- **Ed25519 request signing** for workers (keypair generated on the worker, public key
  registered at enrollment), timestamp + nonce replay protection, server-driven rotation.
- **Consensus-first publication.** Raw observations are internal; only trust-weighted
  aggregates ever leave the platform, pushed to VPS Advisor's Results API.
