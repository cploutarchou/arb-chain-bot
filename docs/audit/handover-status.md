# Audit remediation — handover status

Branch: `claude/triangular-arbitrage-platform-969nkv` (pull request #20, draft,
base `master`). Every commit on the branch is authored by Christos
Ploutarchou and uses the `fix:` / `feat:` / `docs:` / `chore:` / `test:`
prefixes. CI (`.github/workflows/ci.yml`) has been green on every push of
this branch since the test-database cleanup fix; the secret scan is green.

## What is done

- **P0-1, P0-2, P0-3** — all fixed with tests (see
  `implementation-roadmap.md`, "P0 status").
- **P1** — fixed: P1-1 … P1-24 complete (P1-15's engineering half is
  fixed and the backup-automation choice is framed as an open operator
  decision in `docs/decisions/2026-09-10-backup-automation-open-operator-decision.md`).
  P1-24 (console): F1–F8 all implemented — F5 (overview five-second
  test: PnL/drawdown/fees, breakers, feed state, venue clock,
  capital-in-use) and F6 (Paper page as a live-cycle monitor over
  `GET /api/v1/paper/active`, with reason/fees/duration on the persisted
  cycles) landed with migration 000021; lint, typecheck, `next build`
  and the full Playwright suite (47 tests) pass.
- **P2 pulled forward** — F10, F11, F12, F13, F14, F16, M3, M4, M5, O6, O8,
  T2, T4, T5, T6, and now T8 (metadata-diff breaker) and the S4
  `CreateUser` org-placement follow-up; T7 is resolved by a recorded
  decision (`docs/research/fees.md` §"Decision record": operator-configured
  rates; the automatic per-account fetch needs a vault read that is
  write-only by design).
- **Breaker acknowledgement** — `POST /api/v1/risk/breakers/close`
  (ADMIN, CSRF, audited, type-to-confirm) calls `risk.Registry.Close`;
  the Risk Center renders the control beside each open breaker's reason.

## What remains

1. Regression and the final review: run the full suite (below), refresh
   `master-report.md` §ratings and the verdict with measured evidence,
   update `test-plan.md` and `performance-plan.md` with the new tests,
   and update the pull request description.
2. Optional hardening from the P2/P3 backlog (none block the review):
   T10 (screener constraint duplication), D7–D10 database items, S5/S8/
   S10/S11 console-RBAC and consent items, X-series scanner polish,
   O7/O9/O10/O11 observability, F9–F19 console P2s.

## How to resume

```bash
git fetch origin claude/triangular-arbitrage-platform-969nkv
git checkout claude/triangular-arbitrage-platform-969nkv
git status && git log --oneline -15

gofmt -l internal cmd && go vet ./... && golangci-lint run ./...
go test -race -count=1 ./...                     # database-backed tests skip without the env below
# with a disposable PostgreSQL 16 and the migrations applied via psql (make test-db):
ARB_TEST_DATABASE_URL="postgres://arb:<password>@localhost:5432/arb_test?sslmode=disable" \
ARB_TEST_DB_DESTRUCTIVE=1 go test -race -count=1 ./internal/storage/...
cd web && npm ci && npm run lint && npm run typecheck && npm run build
```

Rules that apply to every change on this branch: never `git push --force`,
`git reset --hard` or `git clean -fd`; never submit live trades or touch
live balances (`LiveExecutor` refuses every call by design); money math in
`shopspring/decimal`; one coherent change per commit with tests; merge the
pull request with the custom title `Merge pull request #20 from
cploutarchou/triangular-arbitrage-platform`.
