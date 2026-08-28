GO ?= go
MIGRATE_DSN ?= postgres://arb:arb-dev-password@localhost:5432/arb?sslmode=disable
TEST_DSN ?= postgres://arb:arb-dev-password@localhost:5432/arb_test?sslmode=disable

.PHONY: all build test race lint fmt vet tidy up down migrate create-secret test-db web-install web-dev web-build web-lint clean docker-build record record-stop campaign

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

# Generate the vault master key into .env (keeps an existing one;
# FORCE=1 replaces it — stored secrets then become unreadable).
create-secret:
	./scripts/create-secret.sh

# A disposable database for the storage integration tests, which DELETE
# nearly every table and must never point at a working database. Run the
# suite with:
#   ARB_TEST_DATABASE_URL=$(TEST_DSN) ARB_TEST_DB_DESTRUCTIVE=1 go test -race ./internal/storage/
test-db:
	docker compose exec -T db psql -U arb -d postgres -v ON_ERROR_STOP=1 \
		-c "DROP DATABASE IF EXISTS arb_test" -c "CREATE DATABASE arb_test OWNER arb"
	for f in migrations/*.up.sql; do \
		docker compose exec -T db psql -U arb -d arb_test -v ON_ERROR_STOP=1 -f - < "$$f" || exit 1; \
	done
	@echo "arb_test ready: $(TEST_DSN)"

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

# --- deployment (docs/deployment.md) ---------------------------------------

docker-build:
	docker compose build

# Start recording real Binance depth feeds (db + migrations + recorder).
record:
	docker compose --profile record up -d --build

record-stop:
	docker compose --profile record stop arbd-record

# Run the §80 campaign over a finished recording:
#   make campaign RECORDING=<session-id>
campaign:
	docker compose --profile campaign run --rm campaign \
		-recording $(RECORDING) -dir /recordings/$(RECORDING) \
		-assets USDT -balance USDT=10000 -seeds 1,2,3 \
		-out /recordings/campaign-$(RECORDING)
