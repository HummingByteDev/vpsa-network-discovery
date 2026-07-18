GO      ?= go
LDFLAGS := -X github.com/vpsadvisor/ip-discovery/internal/platform/version.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) \
           -X github.com/vpsadvisor/ip-discovery/internal/platform/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
COMPOSE := docker compose -f deploy/compose/dev.compose.yaml

.PHONY: build test vet tidy check migrate dev-up dev-down dev-logs

build:
	$(GO) build -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

check: vet test build

migrate: build
	CNIP_DB_DSN=$${CNIP_DB_DSN:-postgres://cnip:cnip-dev@localhost:5433/cnip} ./bin/migrate

dev-up:
	$(COMPOSE) up -d --build

dev-down:
	$(COMPOSE) down

dev-logs:
	$(COMPOSE) logs -f
