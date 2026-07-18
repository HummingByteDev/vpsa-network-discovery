# Phase 1 Demo — Foundation

What exists: monorepo scaffold, shared platform packages, PostgreSQL schemas +
migration runner, the mock VPS Advisor stub, service shells for coordinator and
aggregator, and the dev compose environment. (Builder/worker are entrypoint stubs;
their pipelines are Phases 2–5.)

## Prerequisites

Docker + Docker Compose. Go 1.26+ only if you want to run tests/binaries natively.

## 1. Bring up the stack

```sh
make dev-up          # or: docker compose -f deploy/compose/dev.compose.yaml up -d --build
```

Services: `postgres` (host port **5433**), `minio` (9000/9001, bucket
`vapn-artifacts` auto-created), `mockadvisor` (8081), `coordinator` (8080),
`aggregator` (8082). The one-shot `migrate` service applies all schema migrations
before coordinator/aggregator start.

## 2. Verify the test gate

```sh
# health & readiness (readyz proves DB connectivity)
curl localhost:8080/readyz          # → ready
curl localhost:8082/readyz          # → ready

# stub serves the provider contract (A1) from fixtures; auth required
curl -H "Authorization: Bearer dev-advisor-token" \
  "localhost:8081/api/v1/monitoring/providers?enabled=true" | jq .
curl -sw "%{http_code}\n" -o /dev/null localhost:8081/api/v1/monitoring/asns   # → 401

# schemas exist
docker exec vapn-dev-postgres-1 psql -U vapn -d vapn -c "\dt routing.*"

# migrations are idempotent
docker compose -f deploy/compose/dev.compose.yaml run --rm migrate
```

## 3. Native workflow

```sh
make check           # vet + tests + build (binaries in bin/)
make migrate         # apply migrations to the compose postgres (localhost:5433)
```

## 4. Tear down

```sh
make dev-down        # add -v to drop the postgres/minio volumes
```
