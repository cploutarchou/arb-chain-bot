---
description: Run the full regression the CI pipeline runs, including the database-backed tests when a test database is configured
agent: build
---
Run the complete validation for this repository and report every failure
verbatim; fix only what the run proves broken, one commit per fix.

Extra arguments for `go test` (for example `-run TestOutbox`): $ARGUMENTS

1. `gofmt -l internal cmd` (must print nothing), `go vet ./...`, `go build ./...`.
2. `golangci-lint run ./...` (0 issues).
3. `go test -race -count=1 ./...`. If `ARB_TEST_DATABASE_URL` is set, export
   `ARB_TEST_DB_DESTRUCTIVE=1` as well so the storage suite runs against the
   disposable database instead of skipping; `make test-db` creates it with the
   migrations applied through psql, which is how CI applies them.
4. `cd web && npm run lint && npm run typecheck && npm run build`.
5. Benchmarks for the hot path, for the record:
   `go test -run xxx -bench 'BenchmarkQuoteCycle50Levels|BenchmarkSizeSearchCycle50Levels|BenchmarkSizeSearch50Levels' -benchmem -count=3 ./internal/pricing/`.

Summarise: what passed, what failed with the output, what was skipped and
why (a skipped database suite is a gap, not a pass).
