GO      ?= go
LDFLAGS := -X github.com/HummingByteDev/vpsa-network-discovery/internal/platform/version.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) \
           -X github.com/HummingByteDev/vpsa-network-discovery/internal/platform/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
COMPOSE := docker compose -f deploy/compose/dev.compose.yaml

.PHONY: build test vet tidy check migrate dev-up dev-down dev-logs

build:
	$(GO) build -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...

# -p 1: DB integration tests share one database. The default DSN targets the
# dev stack's *vapn_test* database (created by `make test-db`), never the live
# dev `vapn` database — the tests truncate and reshape what they touch.
test:
	VAPN_TEST_DB_DSN=$${VAPN_TEST_DB_DSN:-postgres://vapn:vapn-dev@localhost:5433/vapn_test} \
	$(GO) test -p 1 ./...

test-db:
	docker exec vapn-dev-postgres-1 psql -U vapn -d postgres \
	  -c "create database vapn_test owner vapn" 2>/dev/null || true

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

check: vet test build

migrate: build
	VAPN_DB_DSN=$${VAPN_DB_DSN:-postgres://vapn:vapn-dev@localhost:5433/vapn} ./bin/migrate

dev-up:
	$(COMPOSE) up -d --build

dev-down:
	$(COMPOSE) down

dev-logs:
	$(COMPOSE) logs -f
