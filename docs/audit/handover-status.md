# Audit remediation — handover status

Branch: `claude/triangular-arbitrage-platform-969nkv` (pull request #20, draft,
base `master`). Every commit on the branch is authored by Christos
Ploutarchou and uses the `fix:` / `feat:` / `docs:` / `chore:` / `test:`
prefixes. CI (`.github/workflows/ci.yml`) has been green on every push of
this branch since the test-database cleanup fix; the secret scan is green.

## What is done

- **P0-1, P0-2, P0-3** — all fixed with tests (see
  `implementation-roadmap.md`, "P0 status").
- **P1** — fixed: P1-1 … P1-9, P1-10 … P1-14, P1-16, P1-17, P1-18 … P1-23;
  P1-15 partly (the restore drill fails closed and the managed-database
  backup options are documented; provider automation is an operator
  decision). P1-24 (console) is in progress: F1, F2, F3, F4, F7, F8 are
  implemented on the branch as a snapshot that typechecks and lints;
  F5 (overview five-second test) and F6 (Paper page as a live-cycle
  monitor) are not started.
- **P2 pulled forward** — F10, F11, F12, F13, F14, F16, M3, M4, M5, O6, O8,
  T2, T4, T5, T6 (see "P2 status" in the roadmap).

## What remains

1. Console (P1-24): run `npm run build` in `web/`, review the snapshot
   against `docs/audit/ui-ux-audit.md` F1–F8, then implement F5 and F6.
   The backend now exposes `ABORTED` as a cycle outcome, `queues.paper.dropped`,
   `queues.outbox.write_failures`/`failing`/`unlinked_cycles`, `clock.*`
   in `GET /api/v1/system/health`, `fees_marked` / `fees_by_asset` /
   `fees_unmarked` in `GET /api/v1/pnl`, `memberships` in `GET /api/v1/me`,
   the `X-Org-ID` request header, and `revalidations` /
   `revalidation_rejects` / `invariant_violations` counters.
2. A breaker acknowledgement endpoint: the `daily_loss`, `drawdown`,
   `slippage` and `simulation_inconsistency` breakers stay OPEN until an
   operator closes them, and there is no API for that yet. Add
   `POST /api/v1/risk/breakers/close` (ADMIN, CSRF, audited, type-to-confirm)
   calling `risk.Registry.Close`, and a Risk Center control.
3. Remaining P2/P3 items in `implementation-roadmap.md`, in particular T7
   (fee rates from the venue), T8 (topology and instrument rules refreshed
   on a timer, or at least a metadata diff that opens a breaker), P1-15's
   backup automation (operator decision), and the S4 follow-up that
   `CreateUser` still joins every console account to organisation 1.
4. Regression and the final review: run the full suite (below), refresh
   `master-report.md` §ratings and the verdict with measured evidence,
   update `test-plan.md` and `performance-plan.md` with the new tests and
   the sizer measurements (exact path 1.5–1.6 ms/op, 12 048 allocs; the
   previous search 6.8–8.1 ms/op, 108 231 allocs on the shared host), and
   update the pull request description.

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
