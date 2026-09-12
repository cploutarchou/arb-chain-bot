# Audit remediation — handover status

The remediation programme is complete: PR #20 (the main remediation
branch) is merged at `b385fd8`, the blocking-items follow-up
(S5/S8/S10/S11, backup option B, fresh campaign evidence) is PR #21,
and the hardening follow-up (S12–S15, D7–D10, X4/X5/X7/X8, O7/O9/O10,
T10's arithmetic half, F9/F14/F18) is PR #22 — both merged to master,
closing the Argon2 clamp with
rehash-on-login, the HTTP server envelope, the console-query indexes
(migration 000022), the pool statement_timeout with a warm connection
floor, the retention-clamped and LIMIT-bounded funding query, and the
CHECK bounds on financial columns (migration 000023). Every commit is
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
2. Nothing. The hot-path pass (PR #24: X9, O11, T10's parser half)
   and the placement pass (PR #26: O14's alert rules and the T10/F17
   fee-model merge) closed the last two entries — every audited finding
   from P0 through P3 is closed with its test: S1-S15, D7-D11, X4-X11,
   O1-O3, O5-O15, T2/T4-T8/T10/T11/T12, F1-F19.

## Where engineering resumes next (the audited lists are done)

The programme-level backlog in `docs/PENDING.md` is the queue now.
Closed from it: T-062 (PR #28, 2026-09-12 — `backtest.Result`
rejection-reason histogram with the two pre-gate buckets, the §80
Rejection-reasons table and a NO-CYCLES verdict that names its top
reasons; the dust-floor exit is counted at last) and T-084's remainder
(PR #29, 2026-09-12 — hourly idempotent maturation job in cmd/worker,
the operator's payouts report with the fraud-rule-3 refund rate and
clawback exposure over the affiliate ledger, audited payout recording
validated against the matured balance, platform-admin API + Billing
page section; Paddle refund events still owed with T-083's sandbox
run). Remaining candidates in a sensible order, none blocking another:

- **T-057/059/060/061 console surfaces** — platform settings, operating
  mode, secrets vault, venue capabilities have backend depth the
  console never matched.
- **T-075 remainder** — Upbit, Bithumb, LBank, Phemex collectors, and
  the HTX request-limit answer the rate gate does not classify.
- **T-079 remainder** — scheduled migrations and health-check alerting
  (backups landed with option B).
- **Performance plan items 2–3** — the reservation mutex and AnyOpen's
  per-evaluation allocation on the evaluator path; and the ~7.7 min
  serial `internal/api` race suite.
- **Testing gaps** the final review named: a frontend unit layer,
  down-migration replay, fuzzing.

## How to resume

```bash
git fetch origin
git checkout master && git pull   # all merged follow-up work lives here
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
