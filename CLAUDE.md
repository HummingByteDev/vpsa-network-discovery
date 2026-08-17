````markdown
# CLAUDE.md

# VPS Advisor Community Network Intelligence Platform

## Network Monitoring, Geographic Aggregation, Builder Configuration, and CLI Improvements

You are working on the **VPS Advisor Community Network Intelligence Platform**.

The core platform is already implemented. Artifact publishing is currently working, but the monitoring data being produced is incomplete for the intended VPS Advisor use case.

This task is primarily an **investigation, correction, and extension of the existing implementation**.

Do not redesign the platform from scratch.

Do not replace the existing architecture simply because another implementation may appear simpler.

First understand the existing implementation, data model, pipeline, API contracts, worker behaviour, builder behaviour, and VPS Advisor integration. Then make the architectural changes necessary to correctly support the requirements below.

---

# 1. Project Context

VPS Advisor is an independent VPS/server provider review platform.

Providers can list their services on VPS Advisor, and VPS Advisor collects information about those providers.

The Community Network Intelligence Platform is an independent monitoring system that provides network-level intelligence and uptime measurements for providers listed on VPS Advisor.

The monitoring platform consists broadly of:

- VPS Advisor
- coordinator/API
- community workers
- routing/BGP data
- RIPE RIS data
- builder
- signed routing snapshots
- IP/network probing
- geographic enrichment
- aggregation
- artifact publishing
- PostgreSQL
- object/artifact storage

The platform is designed so that community members can contribute worker infrastructure.

Workers:

1. authenticate with the coordinator;
2. receive the appropriate routing/network snapshot;
3. identify monitoring targets;
4. probe targets;
5. report measurements;
6. participate in aggregated provider/network health calculations.

The VPS Advisor website consumes the resulting aggregated monitoring information.

The VPS Advisor website itself is already implemented.

**Do not rebuild the VPS Advisor website.**

Only modify the VPS Advisor integration where required to support the corrected monitoring data model/API.

---

# 2. Primary Problem

Artifact publishing now works, but the published monitoring data does not contain the geographic information required by VPS Advisor.

For example:

**AS200019 is the ASN for AlexHost SRL.**

The expected geographic distribution of its IPv4 space is approximately:

```text
Moldova: 66.5%
Netherlands: 8.1%
United Kingdom: 5.2%
Sweden: 5.2%
Bulgaria: 4.0%
Switzerland: 4.0%
France: 3.5%
Romania: 2.3%
United States: 1.2%
```
````

These percentages represent the distribution of IPv4 space by country.

The monitoring platform must therefore preserve enough information to derive this distribution.

The current result is:

```python
{
    'as_of': '2026-08-17T11:20:00Z',
    'global': {
        'metrics': {
            'loss_rate': 0.09385113268608414,
            'rtt_p50_ms': 173.8715,
            'rtt_p95_ms': 232.95020000000002,
            'worker_count': 2,
            'dissent_ratio': 0
        },
        'verdict': 'insufficient_data',
        'confidence': 0
    },
    'provider_id': 'alexhost-com'
}
```

This is insufficient.

The current response is overly global and does not provide the geographic breakdown required by the product.

---

# 3. Core Requirement: BGP Prefixes Must Be Geographically Enriched

The essence of the RIPE/BGP + MaxMind pipeline is:

```text
Provider ASN
    ↓
BGP announced prefixes
    ↓
Deduplicate prefixes
    ↓
Determine IP address space represented by prefixes
    ↓
GeoIP lookup
    ↓
Country attribution
    ↓
Aggregate IPv4 address count by country
    ↓
Determine percentage distribution
    ↓
Determine monitoring targets
    ↓
Probe targets from community workers
    ↓
Aggregate probe measurements by country
    ↓
Produce global + regional/country monitoring data
```

The platform must not treat the ASN itself as the monitoring target.

The ASN is used to discover the provider's announced network space.

---

# 4. Example Geographic Dataset

The following is representative of the information the system needs to be able to derive.

```text
| Netblock | Country | Number of IPs |
|----------|---------|---------------|
| 131.123.37.0/24 | Bulgaria | 256 |
| 131.123.38.0/24 | Bulgaria | 256 |
| 131.123.39.0/24 | Bulgaria | 256 |
| 131.123.40.0/24 | Bulgaria | 256 |
| 131.123.41.0/24 | Bulgaria | 256 |
| 131.123.42.0/24 | Bulgaria | 256 |
| 131.123.43.0/24 | Bulgaria | 256 |
| 37.221.65.0/24 | Bulgaria | 256 |
| 131.123.36.0/24 | France | 256 |
| 143.246.208.0/24 | France | 256 |
| 143.246.209.0/24 | France | 256 |
| 143.246.210.0/24 | France | 256 |
| 143.246.211.0/24 | France | 256 |
| 143.246.212.0/24 | France | 256 |
| 131.123.32.0/24 | Moldova | 256 |
| 131.123.34.0/24 | Moldova | 256 |
| 131.123.44.0/22 | Moldova | 1,024 |
| 131.123.48.0/20 | Moldova | 4,096 |
| 143.246.221.0/24 | Moldova | 256 |
| 176.123.0.0/21 | Moldova | 2,048 |
| 176.123.8.0/22 | Moldova | 1,024 |
| 176.125.242.0/23 | Moldova | 512 |
| 37.221.67.0/24 | Moldova | 256 |
| 45.145.0.0/24 | Moldova | 256 |
| 5.63.19.0/24 | Moldova | 256 |
| 80.96.108.0/24 | Moldova | 256 |
| 80.96.112.0/24 | Moldova | 256 |
| 80.96.113.0/24 | Moldova | 256 |
| 80.96.58.0/24 | Moldova | 256 |
| 80.96.59.0/24 | Moldova | 256 |
| 80.96.68.0/24 | Moldova | 256 |
| 80.97.124.0/24 | Moldova | 256 |
| 80.97.128.0/20 | Moldova | 4,096 |
| 81.180.92.0/24 | Moldova | 256 |
| 81.180.93.0/24 | Moldova | 256 |
| 85.120.216.0/24 | Moldova | 256 |
| 85.120.217.0/24 | Moldova | 256 |
| 85.120.252.0/24 | Moldova | 256 |
| 85.120.253.0/24 | Moldova | 256 |
| 85.120.254.0/24 | Moldova | 256 |
| 85.120.81.0/24 | Moldova | 256 |
| 85.121.149.0/24 | Moldova | 256 |
| 85.121.176.0/24 | Moldova | 256 |
| 85.121.177.0/24 | Moldova | 256 |
| 85.121.178.0/24 | Moldova | 256 |
| 85.121.183.0/24 | Moldova | 256 |
| 85.121.4.0/24 | Moldova | 256 |
| 85.121.5.0/24 | Moldova | 256 |
| 85.122.114.0/24 | Moldova | 256 |
| 85.137.249.0/24 | Moldova | 256 |
| 91.208.162.0/24 | Moldova | 256 |
| 91.208.184.0/24 | Moldova | 256 |
| 91.208.197.0/24 | Moldova | 256 |
| 91.208.206.0/24 | Moldova | 256 |
| 132.243.166.0/24 | Netherlands | 256 |
| 132.243.172.0/24 | Netherlands | 256 |
| 176.116.0.0/24 | Netherlands | 256 |
| 45.148.244.0/24 | Netherlands | 256 |
| 45.150.110.0/24 | Netherlands | 256 |
| 45.93.8.0/24 | Netherlands | 256 |
| 5.181.0.0/24 | Netherlands | 256 |
| 5.252.20.0/24 | Netherlands | 256 |
| 85.137.248.0/24 | Netherlands | 256 |
| 93.185.167.0/24 | Netherlands | 256 |
| 146.19.213.0/24 | Russia | 256 |
| 159.253.120.0/24 | Russia | 256 |
| 45.86.86.0/24 | Russia | 256 |
| 45.93.9.0/24 | Russia | 256 |
| 91.199.133.0/24 | Russia | 256 |
| 91.229.239.0/24 | Russia | 256 |
| 94.103.188.0/24 | Russia | 256 |
| 143.246.192.0/24 | Sweden | 256 |
| 143.246.193.0/24 | Sweden | 256 |
| 143.246.194.0/24 | Sweden | 256 |
| 143.246.195.0/24 | Sweden | 256 |
| 143.246.196.0/24 | Sweden | 256 |
| 143.246.197.0/24 | Sweden | 256 |
| 143.246.198.0/24 | Sweden | 256 |
| 143.246.199.0/24 | Sweden | 256 |
| 132.243.161.0/24 | Switzerland | 256 |
| 132.243.162.0/24 | Switzerland | 256 |
| 132.243.174.0/24 | Switzerland | 256 |
| 132.243.175.0/24 | Switzerland | 256 |
| 2.59.219.0/24 | Switzerland | 256 |
| 132.243.173.0/24 | Turkey | 256 |
| 131.123.35.0/24 | United Kingdom | 256 |
| 143.246.216.0/24 | United Kingdom | 256 |
| 143.246.217.0/24 | United Kingdom | 256 |
| 143.246.218.0/24 | United Kingdom | 256 |
| 143.246.219.0/24 | United Kingdom | 256 |
| 171.22.181.0/24 | United Kingdom | 256 |
| 213.111.160.0/22 | United Kingdom | 1,024 |
| 213.111.164.0/22 | United Kingdom | 1,024 |
| 213.111.168.0/22 | United Kingdom | 1,024 |
| 213.111.172.0/22 | United Kingdom | 1,024 |
| 87.120.244.0/24 | United Kingdom | 256 |
| 151.244.219.0/24 | United States | 256 |
| 46.16.34.0/24 | United States | 256 |
| 91.237.119.0/24 | United States | 256 |
```

This is an important conceptual distinction:

**The system is not simply GeoIP-looking up a provider's ASN.**

It is:

> Finding IP prefixes announced by the ASN through BGP, then geographically enriching the resulting IP space.

---

# 5. Geographic Aggregation Requirements

The builder must produce geographic metadata for every monitored provider.

At minimum, the resulting provider data must be capable of representing:

- country
- country code
- IPv4 address count
- IPv4 percentage
- announced prefixes contributing to the country
- monitoring targets associated with the country
- measurement results associated with the country
- measurement timestamp

The implementation may use a different internal model if appropriate, but the resulting information must support the above requirements.

---

# 6. IPv4 Percentage Calculation

For each provider:

```text
total IPv4 addresses announced by provider
```

must be calculated after prefix deduplication.

Then:

```text
country_ipv4_count / total_ipv4_count * 100
```

produces the country share.

For example:

```text
Moldova IPv4 count = 66,500
Total IPv4 count    = 100,000

Moldova share = 66.5%
```

Do not calculate percentages based on:

- number of prefixes alone
- number of /24s alone
- number of BGP observations
- number of peers
- number of workers

A `/20` represents substantially more IPv4 addresses than a `/24`.

The calculation must account for actual address-space size.

---

# 7. Prefix Deduplication

The existing project already addresses duplicate BGP prefixes.

Preserve and verify this behaviour.

The same prefix may appear multiple times because it is observed through:

- multiple peers
- multiple AS paths
- multiple collectors
- multiple BGP observations

A prefix must not be counted repeatedly simply because it appears multiple times in the source data.

The geographic aggregation pipeline must operate on the correct deduplicated network set.

Investigate the existing implementation and ensure:

```text
BGP observations
      ↓
deduplicated provider prefixes
      ↓
GeoIP enrichment
      ↓
address-space calculation
```

rather than:

```text
BGP observations
      ↓
GeoIP enrichment
      ↓
count everything
```

---

# 8. MaxMind GeoIP Integration

The MaxMind GeoIP database is specifically required to geographically enrich the IP space.

Use the existing MaxMind integration where possible.

Do not introduce an unrelated geolocation provider unless there is a demonstrated implementation requirement.

The system must support the existing MaxMind configuration model and documentation.

For each relevant network/prefix, determine the appropriate geographic attribution.

Be careful with the distinction between:

- prefix network address
- individual IP addresses
- MaxMind record boundaries
- countries
- reserved/unallocated space
- unknown locations

Do not silently classify unknown data as a real country.

Where the implementation needs a policy for prefixes spanning multiple geographic records, inspect the existing design and establish a deterministic, documented rule.

Do not invent a simplistic rule without checking how the existing system is designed.

---

# 9. Monitoring Targets Must Be Derived From Network Space

This is a critical requirement.

The monitoring system should not use a single hard-coded IP address as the source of truth for a provider.

The probe target set must be derived from the provider's announced network space.

The conceptual flow is:

```text
Provider
   ↓
ASN
   ↓
BGP prefixes
   ↓
Deduplicated prefixes
   ↓
GeoIP enrichment
   ↓
Country/network groups
   ↓
Probe targets
   ↓
Community workers
```

The target derivation must allow a provider with infrastructure in multiple countries to be monitored across those locations.

---

# 10. Country-Level Monitoring Aggregation

Probe results must not only be aggregated globally.

They must also be aggregated by country/region.

For example:

```text
Provider: AlexHost

Global
  uptime
  packet loss
  RTT
  worker count
  confidence

Moldova
  uptime
  packet loss
  RTT
  worker count
  confidence

Netherlands
  uptime
  packet loss
  RTT
  worker count
  confidence

United Kingdom
  uptime
  packet loss
  RTT
  worker count
  confidence

...
```

The exact API structure should follow the existing architecture and conventions, but it must expose enough information for the frontend to produce regional monitoring visualizations.

---

# 11. Reference Mockups

The following files show how the monitoring data will be consumed:

```text
dev-files/mock/monitored-network.png
dev-files/mock/regional-monitoring.png
```

Inspect these files carefully before implementing the data model/API changes.

They are important product references.

Use them to understand the intended consumer-facing information, particularly:

- global monitoring
- regional/country monitoring
- geographic distribution
- country-level status
- network coverage
- presentation of monitoring results

Do not attempt to redesign the mockups.

Use them to determine what information the backend needs to expose.

If the mockups contain information that the current backend cannot produce, identify the missing data explicitly and implement the required backend support where appropriate.

---

# 12. Current API Response Must Evolve

The current response:

```python
{
    'as_of': '2026-08-17T11:20:00Z',
    'global': {
        'metrics': {
            'loss_rate': 0.09385113268608414,
            'rtt_p50_ms': 173.8715,
            'rtt_p95_ms': 232.95020000000002,
            'worker_count': 2,
            'dissent_ratio': 0
        },
        'verdict': 'insufficient_data',
        'confidence': 0
    },
    'provider_id': 'alexhost-com'
}
```

is insufficient for the intended use case.

Do not simply append arbitrary fields.

Review the existing API contract and determine a coherent structure for:

- global data
- geographic/network distribution
- country-level monitoring
- timestamps
- coverage
- target counts
- measurements
- confidence
- verdict
- worker participation

Maintain backwards compatibility where practical.

If a breaking API change is genuinely necessary, document it and update all consumers.

---

# 13. Distinguish Network Distribution From Monitoring Results

These are related but different concepts.

The system must distinguish:

### Network distribution

Where the provider's announced IPv4 address space is located.

Example:

```text
Moldova: 66.5%
Netherlands: 8.1%
United Kingdom: 5.2%
...
```

### Monitoring results

How the provider's network behaves when probed by community workers.

Example:

```text
Moldova
  packet loss: 1.2%
  p50 RTT: 40ms
  p95 RTT: 85ms

Netherlands
  packet loss: 0.4%
  p50 RTT: 25ms
  p95 RTT: 60ms
```

Do not conflate these.

A country can have a large percentage of IP space but poor measurement coverage.

The API should allow the consumer to understand both.

---

# 14. Coverage and Confidence

The system must preserve the distinction between:

- available network space
- derived monitoring targets
- successfully probed targets
- worker participation
- measurement confidence

Do not report high confidence simply because BGP data exists.

Likewise, do not treat lack of probe data as proof that the provider is down.

The existing verdict/confidence model must be reviewed and adapted to country-level monitoring.

---

# 15. Caddy Port Configuration

The builder currently has a deployment problem when installed on a VPS already running another service on port 80.

The current error is:

```text
Error response from daemon: failed to set up container networking: driver failed programming external connectivity on endpoint vapn-caddy-1 (...): failed to bind host port 0.0.0.0:80/tcp: address already in use
```

The builder deployment must therefore support configurable host ports for Caddy.

The Caddy host port must be configurable through the builder `.env` configuration.

Do not hard-code:

```text
80:80
```

in a way that cannot be changed by the operator.

The configuration should allow a user to select an alternative host port while retaining the appropriate container-side port.

For example, the operator should be able to configure something conceptually like:

```text
CADDY_HTTP_PORT=8080
```

Use the actual project's configuration naming conventions rather than blindly adopting this variable name.

---

# 16. Caddy Port Requirements

The implementation must:

1. expose the Caddy host port through configuration;
2. provide a sensible default;
3. preserve existing behaviour for users who do not customize it;
4. allow installation alongside services already occupying port 80;
5. ensure Docker Compose correctly interpolates the configured port;
6. ensure documentation explains the setting;
7. ensure the installer/configuration flow exposes it appropriately;
8. ensure health checks and dependent services remain functional.

Check whether HTTPS/443 and other ports have similar conflicts.

Do not unnecessarily change them unless the implementation requires it.

---

# 17. `vapn` Reconfiguration / Reinstallation

Add a user-friendly CLI workflow for reconfiguring or reinstalling the VAPN installation.

The desired user experience is conceptually:

```text
vapn reconfigure
```

or:

```text
vapn reinstall
```

Determine the most appropriate command based on the existing CLI conventions.

Do not blindly implement both commands if one clear command is preferable.

The workflow should allow a user to effectively return to the installation/configuration process without manually editing every configuration file.

---

# 18. Reconfiguration Behaviour

The reconfiguration flow should:

1. detect the existing installation;
2. load the current configuration;
3. show existing/default values where appropriate;
4. identify sensitive values safely;
5. prompt the user for required configuration;
6. allow them to retain existing values;
7. allow them to replace values;
8. validate the configuration;
9. safely update the configuration;
10. restart/redeploy affected services when appropriate;
11. verify that the installation is healthy.

The user should not have to remember every configuration value.

---

# 19. Secrets During Reconfiguration

Do not print secrets in plain text unnecessarily without mask them.

When displaying existing values:

```text
Coordinator URL: https://probes.vpsadvisor.com
Snapshot public key: TbP5t********la/rw=
Enrollment token: cwt********Z8YZwsa4
```

or use the project's established secure prompting behaviour.

If a secret is retained, the user should be able to select the existing value without having to re-enter it.

If a new secret is supplied, validate and store it according to the existing security model.

Do not log secrets.

---

# 20. Reinstall vs Reconfigure

Determine whether the current architecture needs two distinct operations.

A reconfiguration operation should normally preserve:

- installation state
- persistent data
- worker identity where appropriate
- snapshots
- credentials that the user elects to retain

A reinstall operation may need to:

- recreate containers
- recreate generated configuration
- pull/update images
- rebuild local state

Do not delete persistent data or credentials without an explicit warning and confirmation.

If the existing project architecture does not support a meaningful distinction, implement the safest and clearest workflow rather than creating two commands with identical behaviour.

---

# 21. Interactive Configuration

The installation/reconfiguration experience should show defaults.

For example:

```text
VPS Advisor URL [https://probes.vpsadvisor.com]:
Worker name [west-t.local]:
...
```

The exact configuration values and prompts must be based on the actual project configuration.

An empty response should retain the displayed default/current value where appropriate.

Sensitive values should use secure input.

---

# 22. Idempotency

The following must be safe:

```text
vapn reconfigure
```

run multiple times.

It must not:

- duplicate configuration entries
- corrupt `.env`
- overwrite unrelated settings
- create duplicate PATH entries
- generate unnecessary new credentials
- destroy persistent state

---

# 23. Documentation Updates

Update all relevant documentation to reflect:

### Geographic monitoring

Explain:

- how BGP prefixes are obtained
- how MaxMind is used
- how IP space is attributed to countries
- how IPv4 percentages are calculated
- how monitoring targets are derived
- how country-level measurements are aggregated

### Caddy

Document:

- default Caddy port
- how to change it
- how to resolve port conflicts
- how to configure it during installation/reconfiguration

### CLI

Document:

- installation
- reconfiguration
- reinstallation if implemented
- what happens to existing configuration
- what happens to persistent data
- how secrets are handled
- verification

### API

Update API documentation with the corrected geographic/monitoring response.

### Worker

Update worker documentation if the target or snapshot contract changes.

Do not duplicate large configuration references unnecessarily.

Follow the existing documentation consolidation principles.

---

# 24. Changelog

Create a comprehensive changelog entry for this release/change.

The changelog must clearly document:

## Added

- geographic network aggregation
- country-level monitoring
- IPv4 country distribution
- new API information
- Caddy port configuration
- CLI reconfiguration/reinstallation capability

## Changed

- monitoring target derivation
- artifact/snapshot contents where applicable
- monitoring aggregation
- API response
- builder configuration

## Fixed

- missing geographic information
- insufficient provider monitoring response
- Caddy port collision issue
- any related issues discovered during implementation

## Compatibility

Document:

- API compatibility
- snapshot compatibility
- worker compatibility
- configuration migration requirements
- upgrade requirements

Do not claim compatibility unless verified.

---

# 25. Tests

Add comprehensive automated tests.

At minimum cover:

## BGP

- ASN prefix discovery
- duplicate prefix removal
- IPv4 address counting
- prefix-size calculation
- provider filtering

## GeoIP

- country lookup
- country aggregation
- unknown location handling
- IPv4 percentage calculation

## Geographic monitoring

- provider with one country
- provider with multiple countries
- country with no successful probes
- country with insufficient workers
- country with partial probe coverage

## API

Verify that:

- global metrics remain available;
- country/network information is returned;
- timestamps are correct;
- provider IDs are correct;
- confidence/verdict values remain coherent.

## Builder

Test:

- geographic artifact generation
- snapshot publication
- configuration loading
- Caddy port configuration
- existing default behaviour

## CLI

Test:

- reconfiguration
- repeated reconfiguration
- existing configuration preservation
- default values
- secret handling
- validation failures
- reinstall behaviour if implemented

---

# 26. Performance Considerations

Do not implement geographic enrichment by expanding every large prefix into millions of individual IP addresses unless the existing architecture explicitly requires it.

For example:

```text
10.0.0.0/8
```

contains over 16 million IPv4 addresses.

The system should operate efficiently on network prefixes and address ranges.

Use the existing architecture's most efficient representation for:

- prefix size
- GeoIP lookup
- country aggregation
- target selection

The geographic aggregation must scale to the number of prefixes expected from real-world ASNs.

---

# 27. Data Integrity

Ensure that geographic aggregation does not introduce duplicate address-space counting.

Pay particular attention to:

- overlapping prefixes
- duplicate BGP observations
- multiple AS paths
- multiple peers
- more-specific prefixes
- less-specific prefixes
- withdrawn routes
- stale routes
- prefixes belonging to other ASNs

The final provider network set must reflect the intended BGP selection/filtering rules already established by the project.

Do not casually change route-selection semantics.

---

# 28. Do Not Make the ASN the Geographic Record

An ASN can announce network space in many countries.

Therefore:

```text
ASN → Country
```

is not an adequate data model.

The relationship is closer to:

```text
ASN
 └── Prefix
      └── Address space
           └── GeoIP location
                └── Country
```

Monitoring targets should then be associated with the relevant network/country context.

---

# 29. Inspect Existing Implementation Before Changing It

Before implementation:

1. inspect the builder;
2. inspect the routing snapshot schema;
3. inspect the BGP parser;
4. inspect prefix deduplication;
5. inspect MaxMind integration;
6. inspect target derivation;
7. inspect worker probing;
8. inspect measurement storage;
9. inspect aggregation;
10. inspect artifact publishing;
11. inspect coordinator API;
12. inspect VPS Advisor integration;
13. inspect the existing CLI;
14. inspect Docker Compose;
15. inspect `.env` handling;
16. inspect documentation;
17. inspect the two mock images.

Identify exactly where the current implementation loses geographic information.

Do not simply add a new parallel pipeline.

Prefer extending the existing pipeline where appropriate.

---

# 30. Preserve Existing Architecture

The following existing capabilities must continue working:

- RIPE RIS collection
- BGP parsing
- prefix deduplication
- provider ASN retrieval
- snapshot generation
- snapshot signing
- artifact publishing
- worker registration
- worker authentication
- worker approval
- worker probing
- measurement reporting
- aggregation
- VPS Advisor integration
- Docker deployment
- PostgreSQL
- object storage

Do not regress existing functionality.

---

# 31. Final Validation

Before considering this task complete, perform an end-to-end test using a real or representative provider ASN.

Use **AS200019** as the primary validation case if the required data is available in the project's test/development environment.

The expected pipeline should demonstrate:

```text
AS200019
    ↓
BGP prefixes
    ↓
deduplicated prefixes
    ↓
IPv4 address-space calculation
    ↓
MaxMind enrichment
    ↓
country distribution
    ↓
country monitoring targets
    ↓
worker probes
    ↓
country-level aggregation
    ↓
global aggregation
    ↓
published artifact/API
```

The resulting API must contain enough information to reproduce the intended data shown in:

```text
dev-files/mock/monitored-network.png
dev-files/mock/regional-monitoring.png
```

Do not require the exact visual frontend implementation as part of this task.

---

# 32. Final Report

After implementation, provide a detailed report containing:

## Root Cause

Explain why geographic information was missing from the current artifact/API.

## Architecture Changes

Explain how geographic information now flows through the system.

## Data Model Changes

Explain any changes to:

- snapshots
- artifacts
- database models
- API schemas
- measurements

## Monitoring Changes

Explain:

- target derivation
- country association
- country-level aggregation
- global aggregation
- confidence/verdict handling

## Builder Changes

Explain:

- Caddy configuration
- environment changes
- build pipeline changes

## CLI Changes

Explain:

- reconfigure/reinstall workflow
- configuration preservation
- secret handling

## Documentation

List every documentation file updated.

## Changelog

Summarize the changelog entry.

## Tests

List tests performed and their results.

## Compatibility

Clearly state:

- worker compatibility
- snapshot compatibility
- API compatibility
- upgrade requirements
- migration requirements

## Remaining Concerns

Identify anything that still requires attention.

---

# 33. Most Important Principles

Follow these principles throughout the implementation:

1. **Investigate before changing code.**
2. **Use the existing architecture wherever possible.**
3. **Do not treat an ASN as a single geographic location.**
4. **Use BGP-announced prefixes as the network source of truth.**
5. **Use MaxMind to geographically enrich the announced address space.**
6. **Calculate geographic distribution using actual IPv4 address counts, not prefix counts.**
7. **Do not double-count duplicate or overlapping network data.**
8. **Probe multiple targets rather than relying on one IP as a provider's source of truth.**
9. **Aggregate monitoring results globally and geographically.**
10. **Do not confuse network distribution with monitoring performance.**
11. **Do not expose secrets.**
12. **Make configuration idempotent.**
13. **Make the builder easy to run alongside existing services.**
14. **Do not break existing workers or snapshots unnecessarily.**
15. **Update documentation and changelog as part of the implementation.**
16. **Use the mockups as product requirements for the data that the backend must provide.**
17. **Do not invent behaviour that is not supported by the existing architecture.**
18. **Prefer a clean, maintainable implementation over a quick patch.**

The end result should provide VPS Advisor with meaningful network intelligence such as:

```text
Provider
  ├── Global network status
  ├── IPv4 network distribution
  │    ├── Moldova
  │    ├── Netherlands
  │    ├── United Kingdom
  │    └── ...
  ├── Regional monitoring status
  │    ├── Moldova
  │    ├── Netherlands
  │    ├── United Kingdom
  │    └── ...
  └── Timestamped measurements
```

rather than only a single global record such as:

```python
{
    "global": {
        "metrics": {...},
        "verdict": "insufficient_data",
        "confidence": 0
    }
}
```

The primary objective is to make the monitoring platform capable of answering:

> **Where does this provider's network exist, how much IPv4 space does it have in each country, and how is that network performing from the perspective of participating community workers?**
