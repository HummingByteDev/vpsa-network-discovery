# Development

For contributors and maintainers. How the repository is organized, how to build
and test, the standards code is held to, and how changes flow from a branch to a
release. New to the *system*? Read [Core Concepts](../concepts/README.md) and the
[Architecture](../architecture/README.md) first — this guide assumes you know
what the components do.

## Quick start for contributors

```sh
git clone https://github.com/HummingByteDev/vpsa-network-discovery.git
cd vpsa-network-discovery

make dev-up      # full local stack (see below)
make test-db     # one-time: create the vapn_test database
make check       # vet + tests + build — run this before every commit
```

`make dev-up` brings up the whole loop locally via
[`deploy/compose/dev.compose.yaml`](../../deploy/compose/dev.compose.yaml):
PostgreSQL (`:5433`), MinIO (artifact store), a **mock VPS Advisor**
(`internal/mockadvisor`, serving the [integration contract](../integration/README.md)
from fixtures), the coordinator, the aggregator, and a few workers. Snapshot
signing keys come from `./bin/keygen`. It runs fully offline — the pre-
downloaded `data/` (RIS bview + GeoLite2) is mounted into the builder, so no
RIPE or MaxMind fetch is needed. Follow the guided tour in
[`docs/demos/phase1.md`](../demos/phase1.md) onward.

## Repository structure

```
cmd/            one main package per binary
  coordinator/  worker-facing API + scheduler        (long-running)
  aggregator/   consensus + trust + publisher         (long-running)
  builder/      RIPE→PostgreSQL→signed artifact        (batch job)
  worker/       the community probe agent              (container)
  vapn/         worker operator CLI (install/status/…) 
  vapnctl/      platform admin CLI
  migrate/      migration runner
  mockadvisor/  stub of the VPS Advisor contract (dev/CI)
  keygen/       Ed25519 keypair generator
  loadtest/     synthetic fleet load generator
internal/       implementation packages (each has a package doc comment)
  advisor/      VPS Advisor client (pull catalog, push results)
  aggregate/    consensus + anomaly detection
  artifact/     SQLite export, manifest, signing, store (S3/fs)
  builder/      snapshot build pipeline + RIS download
  coordinator/  HTTP handlers, scheduler, admin, sync, rotation
  observation/  observation types + validation
  probe/        protocol-agnostic prober; ICMP implementation
  publisher/    outbox drain to VPS Advisor
  registry/     worker registry
  routing/      bogon filter, geo lookup, MRT reader
  scheduler/    assignment leasing
  trust/        trust scoring
  wireauth/     Ed25519 request signing/verification
  platform/     shared infra: db, config, logging, metrics, migrate, http, audit, version
migrations/     one ordered SQL migration stream for the whole DB
deploy/         compose (dev) · prod (production stack) · worker (installer + systemd)
docs/           this documentation
data/           pre-downloaded RIS + GeoLite2 (dev convenience)
```

The mapping to subsystems is in
[architecture 07 §1](../architecture/07-deployment.md#1-artifacts).

## Architecture principles & design philosophy

These are the invariants contributions must preserve — they're what make the
system trustworthy, not incidental style:

1. **VPS Advisor is the only source of truth for providers.** Never build a
   provider registry here; cache with provenance and drop on delist.
2. **Only aggregated consensus leaves the platform.** A single worker's
   observation is never public. `ProviderStatus` is a pure function of
   `ConsensusWindow`s, never of raw observations.
3. **Workers are the hostile edge.** Assume malicious workers, stolen keys, and
   bad measurements. Sign, timestamp, nonce, weight by trust, and require
   consensus. Services trust each other (same zone); workers are never trusted.
4. **The builder is the only MRT/MaxMind reader.** Workers consume finished,
   signed artifacts. Never move parsing to the edge.
5. **Never guess on ambiguity.** MOAS conflicts are flagged, not resolved;
   thin data yields `insufficient_data`, not a verdict; admins outrank
   automation.
6. **Protocol-agnostic measurement.** New probe types slot behind the `Prober`
   interface and the typed `metrics` JSON without schema or scheduler changes.
7. **Idempotency and at-least-once everywhere data crosses a boundary.** Batch
   ids, outbox with retries, idempotent website endpoints.
8. **Everything containerized; 12-factor config.** `VAPN_`-prefixed env vars;
   every service prints its effective config (secrets redacted) at boot.

If a change would violate one of these, it needs an explicit design discussion
first — they're load-bearing.

## Coding standards

- **Go**, formatted with `gofmt`; `go vet` clean (`make vet`). Match the
  surrounding code's naming and comment density.
- **Package docs.** Every `internal/*` package carries a doc comment explaining
  its responsibility and boundary — keep it accurate when you change behavior.
- **Errors** are wrapped with context (`fmt.Errorf("...: %w", err)`).
- **Config** goes through `internal/platform/config` (`VAPN_` prefix, typed
  accessors, redacted dump) — never read `os.Getenv` directly in a service.
- **Logging** via `log/slog` (structured); no secrets in logs, ever.
- **SQL** lives in migrations (`migrations/NNNN_*.sql`), one ordered stream;
  queries are parameterized. Migrations must be backward-compatible one version
  back (expand → migrate → contract) so replicas can roll.
- **Least privilege:** one Postgres role per service (builder can't touch
  `measurements`; coordinator can't write `aggregation`; etc.).

## Testing

```sh
make test        # go test -p 1 ./...  (DB tests share one database, serialized)
make check       # vet + test + build
```

- DB integration tests target the **`vapn_test`** database (created by
  `make test-db`), **never** the live dev `vapn` database — they truncate and
  reshape what they touch. The default DSN is
  `postgres://vapn:vapn-dev@localhost:5433/vapn_test`; override with
  `VAPN_TEST_DB_DSN`.
- Unit tests live beside code (`*_test.go`). Notable suites: `aggregate` (the
  consensus SQL), `wireauth` (signing/replay), `mrtreader`, `bogon`,
  `coordinator` (security, scheduler simulation), `mockadvisor` (contract).
- **Contract parity:** the `mockadvisor` tests encode the
  [integration contract](../integration/django-integration.md) — the same
  expectations the website's staging must satisfy.
- Add tests with behavior changes; consensus/trust/auth changes especially need
  them (they're the trust-critical core).

## Branching, CI/CD & releases

- **Branching:** feature branches off `main`; open a PR; `main` stays green.
  Never commit or push directly to `main` without review.
- **Commits:** clear, imperative subject lines; explain *why* in the body when
  it's not obvious.
- **CI** (`.github/`) runs `make check` (vet + tests against `vapn_test` +
  build) and the contract tests on every PR.
- **Releases:** milestone/tag-based. Images are built per component
  (`coordinator`, `aggregator`, `builder`, `worker`, `migrate`) and published to
  the registry; the worker binary is published to GitHub releases for
  `install.sh`. The version is stamped via `-ldflags` from `git describe` (see
  the [Makefile](../../Makefile)). Full process and gates:
  [release management](../operations/release-management.md) and the
  [launch checklist](../operations/launch-checklist.md).

## Contribution guide

1. **Open an issue** describing the change and, for anything touching the
   invariants above, the design.
2. **Branch, build, test:** `make check` must pass; add tests for behavior
   changes.
3. **Keep docs in sync.** If you change a command, config var, endpoint, or a
   subsystem's behavior, update the relevant doc (this docs tree is treated as
   part of the code — see the [reference](../reference/README.md) pages).
4. **Open a PR** with a clear description and rationale; link the issue.
5. **Review** focuses on correctness, the invariants, and test coverage of the
   trust-critical paths.

Where to go next: [Reference](../reference/README.md) for CLI/config/schema
details, [Operations](../operations/README.md) for how it runs in production,
and the [Handbook](../handbook/README.md) for the whole system in one narrative.
