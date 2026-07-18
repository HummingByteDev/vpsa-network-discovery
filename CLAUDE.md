# CLAUDE.md

# VPS Advisor Community Network Intelligence Platform

## Project Background

This repository is **NOT** the VPS Advisor website.

The VPS Advisor website already exists and is in active production.

This project is a completely independent backend service responsible for providing **network intelligence and provider health data** that will later be consumed by the VPS Advisor website.

The purpose of this repository is to build the complete distributed network monitoring ecosystem that powers the provider uptime information displayed on VPS Advisor.

Think of this project as the "network observability backend" of VPS Advisor.

The VPS Advisor website will become a consumer of this project's APIs.

---

# About VPS Advisor

VPS Advisor is an independent cloud and VPS provider review platform.

Providers can:

- create an account
- claim their company
- manage their listings
- publish products
- receive reviews
- compare against competitors

One of the major features planned for VPS Advisor is **Provider Network Health**.

Unlike traditional uptime websites, VPS Advisor does **NOT** monitor individual customer servers.

Instead, VPS Advisor measures the health of the provider's public network.

The objective is to answer questions such as:

- Is this provider's network healthy?
- How reliable is this provider globally?
- How reliable is this provider in my region?
- Has this provider experienced routing instability recently?
- How has this provider performed historically?

The implementation should always keep this objective in mind.

---

# What We Are Monitoring

This project **DOES NOT** crawl the Internet looking for providers.

Instead, VPS Advisor is the authoritative source.

The VPS Advisor website already contains the provider database.

Each provider record may contain one or more Autonomous System Numbers (ASNs).

Only providers listed on VPS Advisor should ever be monitored.

If a provider does not exist on VPS Advisor, it does not exist to this project.

This greatly simplifies the monitoring scope.

---

# Source of Truth

The VPS Advisor website is the authoritative source for:

- Providers
- Provider IDs
- Company information
- ASN ownership
- Monitoring status
- Provider configuration

This project must never maintain its own provider registry.

Instead, worker infrastructure consumes provider information from VPS Advisor.

---

# Provider Discovery

This project will not scan the Internet for ASNs.

Instead, VPS Advisor will expose authenticated API endpoints that return providers eligible for monitoring.

Example information returned:

- Provider ID
- Provider Name
- ASN(s)
- Monitoring enabled
- Priority
- Additional future metadata

The monitoring platform only stores the returned ASN information necessary for routing intelligence.

No duplicate provider database should exist inside this project.

---

# High-Level Architecture

The overall architecture consists of two independent systems.

```
                    VPS Advisor Website
                           │
                           │
          Provider API / Assignment API
                           │
                           ▼
      Community Network Intelligence Platform
                           │
       ┌───────────────────┼────────────────────┐
       │                   │                    │
       ▼                   ▼                    ▼
 Snapshot Builder     Worker Network     Aggregation Engine
       │                   │                    │
       └───────────────────┼────────────────────┘
                           │
                           ▼
                 Results API (VPS Advisor)
```

The VPS Advisor website remains responsible for presentation.

This project remains responsible for measurement.

---

# Scope

This repository is responsible for:

- Routing intelligence
- ASN ownership
- BGP processing
- Probe scheduling
- Worker management
- Measurement execution
- Consensus
- Trust
- Reputation
- Aggregation
- APIs
- Snapshot publishing

This repository is **NOT** responsible for:

- Provider profiles
- Reviews
- User accounts
- Billing
- Provider management
- Website frontend

Those already exist.

---

# Routing Intelligence

Routing information originates from RIPE RIS.

The implementation should create a routing intelligence database containing only prefixes belonging to providers monitored by VPS Advisor.

Example workflow:

```
VPS Advisor

↓

Retrieve monitored ASN list

↓

Download RIPE RIS snapshot

↓

Extract prefixes belonging only to monitored ASNs

↓

Deduplicate

↓

Build PostgreSQL snapshot

↓

Publish
```

Do not build a database containing every ASN on the Internet if only a subset is needed.

Design the architecture so this optimisation happens naturally.

---

# Snapshot Builder

Implement a dedicated snapshot builder.

Responsibilities include:

- download RIPE snapshots
- obtain monitored ASN list
- extract matching prefixes
- remove duplicate prefixes
- validate routing ownership
- enrich routing data
- build PostgreSQL database
- version snapshots
- generate metadata
- publish snapshots

Workers consume these published snapshots.

Workers never parse MRT files.

---

# PostgreSQL

Use PostgreSQL throughout the project.

Design schemas suitable for:

- routing intelligence
- measurements
- worker registry
- reputation
- scheduling
- snapshot management

Take advantage of PostgreSQL features whenever appropriate.

---

# GeoIP

Integrate MaxMind GeoIP.

GeoIP should enrich routing intelligence.

GeoIP updates should be independent from routing snapshot updates.

---

# Worker Nodes

Worker nodes are Docker containers.

Workers are intended to be run by the community.

Workers should automatically:

- authenticate
- register
- download routing snapshot
- download GeoIP database
- request assignments
- execute measurements
- upload signed observations
- receive configuration updates

Workers should require minimal configuration.

---

# Probe Engine

The probe engine should be protocol agnostic.

ICMP is only one measurement type.

The architecture should support future protocols without redesign.

Workers execute assignments received from VPS Advisor.

Workers never choose probe targets themselves.

---

# Aggregation

Multiple workers measure the same provider.

Public status should always originate from aggregated consensus.

Individual worker observations should never become public results directly.

The aggregation engine should calculate:

- health
- confidence
- regional status
- latency statistics
- packet loss
- anomaly detection
- future network metrics

---

# Trust Model

Workers continuously build trust.

Trust should influence measurement weighting.

The implementation should support:

- reputation
- suspension
- approval
- quarantine
- retirement
- credential rotation

Administrators should always remain in control.

---

# VPS Advisor Integration

The implementation must include comprehensive documentation describing every endpoint that should exist on the VPS Advisor website.

Examples include (but are not limited to):

## Provider APIs

- monitored providers
- provider ASN lookup
- monitoring configuration
- provider priority

## Authentication APIs

- worker registration
- worker approval
- authentication
- key rotation
- credential renewal

## Assignment APIs

- assignment retrieval
- heartbeat
- configuration
- software version

## Result APIs

- measurement upload
- aggregated status upload
- worker diagnostics

## Administration APIs

- worker management
- snapshot management
- trust management
- assignment management

Claude should determine every required endpoint and produce comprehensive API specifications.

---

# VPS Advisor Changes

Although the VPS Advisor website already exists, this project requires new integration points.

Claude should identify every modification required on the VPS Advisor website.

Examples include:

- new database models
- new APIs
- new authentication flows
- new dashboard pages
- new administration pages
- new permissions
- new scheduled tasks
- new background jobs
- new notification events

Document everything comprehensively.

Do not implement the website.

Produce implementation documentation for the website team.

---

# Worker Management

The VPS Advisor administration dashboard should become the control centre for the worker network.

Claude should document every management capability required.

Examples include:

- worker approval
- suspension
- retirement
- diagnostics
- software version monitoring
- trust monitoring
- routing snapshot status
- measurement analytics
- security events
- audit logs

---

# Security

Security is a first-class concern.

Design assuming:

- malicious workers exist
- compromised credentials exist
- unreliable measurements exist

The implementation should support:

- signed communication
- replay protection
- credential rotation
- trust scoring
- consensus
- audit logging

---

# Docker

Every deployable component should be containerised.

Developer experience is important.

The project should support:

- local development
- production deployment
- orchestration
- upgrades

---

# Documentation

Documentation is as important as the implementation.

Claude should produce comprehensive documentation covering:

## Architecture

- complete architecture
- component interaction
- communication flow
- trust model
- routing lifecycle
- measurement lifecycle

## Installation

Document every component individually.

## Builder

Document:

- setup
- operation
- updates
- publishing
- troubleshooting

## Worker

Document:

- installation
- authentication
- lifecycle
- updating
- diagnostics

## API Documentation

Produce comprehensive API documentation.

Every endpoint should contain:

- purpose
- authentication
- request schema
- response schema
- examples
- status codes
- error handling

## VPS Advisor Integration Guide

Produce a dedicated integration document for the VPS Advisor website.

It should describe:

- required backend changes
- required API endpoints
- required database additions
- required dashboard additions
- deployment considerations
- operational workflow

This document should be detailed enough that another engineering team could implement the website integration independently.

## Security Documentation

Document:

- authentication
- trust model
- threat model
- worker lifecycle
- credential rotation
- compromise response

## Operations Documentation

Document:

- upgrades
- disaster recovery
- backup
- monitoring
- troubleshooting
- incident response

---

# Implementation Strategy

Do **NOT** begin coding immediately.

First perform a complete architectural analysis.

Produce:

1. System architecture
2. Component boundaries
3. Service responsibilities
4. Domain model
5. Database design
6. API contracts
7. Security model
8. Trust model
9. Worker lifecycle
10. Snapshot lifecycle
11. Deployment architecture
12. Risk assessment
13. Phased implementation roadmap

Only after the architecture has been approved should implementation begin.

Implementation must be milestone-based.

Each milestone should produce a functional, testable subsystem.

Suggested phases include:

- Phase 1: Architecture & Foundation
- Phase 2: Routing Snapshot Builder
- Phase 3: Snapshot Distribution
- Phase 4: Worker Framework
- Phase 5: Probe Engine
- Phase 6: Authentication & Trust
- Phase 7: Scheduler & Assignments
- Phase 8: Aggregation Engine
- Phase 9: VPS Advisor Integration
- Phase 10: Administration & Operations
- Phase 11: Testing, Documentation & Production Readiness

Claude should refine and expand these phases where appropriate.

---

# Final Goal

The finished project should resemble a mature, production-ready, open-source distributed Internet observability platform rather than a simple uptime checker.

Every architectural decision should prioritise:

- scalability
- maintainability
- security
- modularity
- observability
- operational excellence
- future extensibility
- excellent documentation

Assume this project will become the authoritative network intelligence platform powering VPS Advisor for many years and may eventually support tens of thousands of providers and thousands of community-operated worker nodes worldwide.

# Note

RIPE RIS data and Maxmind GeoIP data has already been downloaded in `data/` directory to save time during development.
