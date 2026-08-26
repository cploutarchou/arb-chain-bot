GO ?= go
MIGRATE_DSN ?= postgres://arb:arb-dev-password@localhost:5432/arb?sslmode=disable

.PHONY: all build test race lint fmt vet tidy up down migrate web-install web-dev web-build web-lint clean

all: fmt vet test build

build:
	$(GO) build ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

bench:
	$(GO) test -bench=. -benchmem -run=^$$ ./...

fmt:
	gofmt -l -w cmd internal

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./...

tidy:
	$(GO) mod tidy

# --- local infrastructure -------------------------------------------------

up:
	docker compose up -d db

down:
	docker compose down

# Applies SQL migrations in order using the migrate container image.
migrate:
	docker compose run --rm migrate

# --- web console ----------------------------------------------------------

web-install:
	cd web && npm install

web-dev:
	cd web && npm run dev

web-build:
	cd web && npm run build

web-lint:
	cd web && npm run lint && npm run typecheck

clean:
	rm -rf bin dist coverage.out
