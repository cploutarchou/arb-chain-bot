# Audit remediation — handover status

The remediation programme is complete: PR #20 (the main remediation
branch) is merged at `b385fd8`, and the blocking-items follow-up branch
(S5/S8/S10/S11, backup option B, fresh campaign evidence) is
`claude/blocking-hardening-s5-s8-s10-s11-backup`. Every commit is
authored by Christos Ploutarchou with the repo's commit prefixes, one
coherent change per commit, tests included.

## What is done

- **P0-1, P0-2, P0-3** — fixed with tests (`implementation-roadmap.md`,
  "P0 status").
- **P1** — all 24 fixed, including P1-24's F1–F8 (F5 overview
  five-second test, F6 Paper live-cycle monitor over
  `GET /api/v1/paper/active`, migration 000021 for the cycle close
  reason) and P1-15 decided and implemented (backup option B: daily
  `pg_dump` CronJob + `pgdump` restore-drill source, drill exercised
  green against a local PostgreSQL 16 — see
  `docs/decisions/2026-09-10-backup-automation-option-b-logical-dump.md`).
- **Breaker acknowledgement** — `POST /api/v1/risk/breakers/close`
  (ADMIN, CSRF, audited, type-to-confirm) + the Risk Center control.
- **P2 pulled forward** — T2, T4, T5, T6, T8 (metadata-diff breaker),
  F10–F14, F16, M3–M5, O6, O8, the S4 CreateUser org placement, and the
  security block S5/S8/S10/S11 (operator-only surfaces behind
  platform_admin with the flag following the role; vault fully
  operator-only; org-roster consent and disclosure guards; per-topic
  WebSocket authorization). T7 is resolved by a recorded decision
  (`docs/research/fees.md` §"Decision record").
- **Evidence** — `master-report.md` §"FINAL PLATFORM REVIEW" (+ its
  addendum) carries the per-area ratings, the measured before/after
  (sizer 108 231 → 12 048 allocs/op) and the verdict. A fresh campaign
  over a live 5-minute Binance recording on the remediated engine found
  zero qualified opportunities in 24 scenarios
  (`docs/campaigns/01M25GET9C8XVKD9358JT15BNC/`) — an honest negative;
  no profitability claim exists anywhere in the repository.

## What remains (none of it blocks the record)

1. The verdict's two remaining conditions are not engineering gaps: a
   campaign showing a positive net edge (the market has not provided
   one — the fee wall rejected everything in the latest window), and the
   production-execution-gate review (legal, compliance, insurance) the
   operator must record in `docs/decisions/`.
2. Optional P2/P3 hardening: T10 (screener constraint duplication),
   D7–D10 database items, S12–S15 P3 security (Argon2 clamp, HTTP
   server timeouts, CI pinning/npm audit, marketing headers), X-series
   scanner polish, O7/O9–O11 observability, F9–F19 console P2s.

## How to resume

```bash
git fetch origin
git checkout claude/blocking-hardening-s5-s8-s10-s11-backup   # or master, post-merge
git status && git log --oneline -10

gofmt -l internal cmd && go vet ./... && golangci-lint run ./...
go test -race -count=1 ./...                     # database-backed tests skip without the env below
# with a disposable PostgreSQL 16 and the migrations applied via psql (make test-db):
ARB_TEST_DATABASE_URL="postgres://arb:<password>@localhost:5432/arb_test?sslmode=disable" \
ARB_TEST_DB_DESTRUCTIVE=1 go test -race -count=1 ./internal/storage/...
cd web && npm run lint && npm run typecheck && npm run build
./scripts/e2e.sh                                  # 47 tests against a real backend
```

Rules that apply to every change on any branch here: never
`git push --force`, `git reset --hard` or `git clean -fd`; never submit
live trades or touch live balances (`LiveExecutor` refuses every call by
design); money math in `shopspring/decimal`; one coherent change per
commit with tests; drafts on the working branch; the merge title is
`Merge pull request #<n> from cploutarchou/<branch>`.
