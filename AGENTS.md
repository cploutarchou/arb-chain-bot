# arb-chain-bot — working instructions

Read this before changing anything. It applies to every coding tool that
reads it (OpenCode reads it directly; the same rules are mirrored in the
skills under `.claude/skills/`).

## What this repository is

A research and paper-trading platform for cryptocurrency triangular
arbitrage: a Go 1.25 monolith (`cmd/arbd`, packages under `internal/`),
PostgreSQL 16 with forward-only SQL migrations (`migrations/`), a Next.js
operations console (`web/`), Helm/Terraform/compose delivery (`deploy/`),
and the design and audit record under `docs/`. Live order submission is
disabled by construction: `execution.LiveExecutor` refuses every call,
`platform.mode LIVE` is rejected, and no change may weaken that.

Maps to read first:
- `docs/audit/architecture.md` — the stage-by-stage trading flow.
- `docs/audit/master-report.md` and `docs/audit/implementation-roadmap.md` —
  the audit findings, their status, and what remains.
- `docs/audit/handover-status.md` — the current state of the remediation
  branch and how to resume it.
- `docs/MASTER_PLAN.md`, `docs/PENDING.md` — the programme plan and open items.
- `.claude/skills/triangular-arbitrage-platform/resources/` — the reference
  material (architecture, order book, triangular math, execution simulation,
  risk management, security, observability, testing, client area, Telegram).

## Non-negotiables

- Never submit a live trade, withdraw or transfer assets, rotate credentials,
  delete exchange accounts, delete production data or modify live balances.
- Never `git push --force`, `git reset --hard` or `git clean -fd`. Never
  rewrite history on a shared branch. Commit or push only when asked.
- Money math uses `github.com/shopspring/decimal` (division precision 28).
  Never a `float64` in any value that reaches a decision, a ledger, or a
  persisted row; floats are for exposition (metrics gauges, charts) only.
- Every stage that can lose or distort a financial record must count and
  surface it: dropped queue items, refused writes, unlinked rows, aborted
  cycles. Silent loss is a defect.
- Fees, buffers, depth and quantization are part of every profitability
  figure. Nothing is described as profitable without a report under
  `docs/campaigns/` and a stated verdict; the phrase "guaranteed profitable"
  never appears.
- Security: never log or return secrets; RBAC is enforced in the backend,
  never by hiding buttons; tenant scope comes from the request context.

## Commit and pull-request conventions

- Commit as the repository owner's git identity. Messages use the prefixes
  `fix:`, `feat:`, `refactor:`, `test:`, `docs:`, `chore:` and describe the
  change and its engineering rationale; no tool or authorship attribution
  trailers, no co-author trailers. Examples:
  `fix: account for taker fees in arbitrage profitability`,
  `feat: add execution-risk guard for stale opportunities`,
  `refactor: centralize exchange market constraints`,
  `test: add order book depth execution scenarios`.
- One coherent change per commit, with its tests. Preserve existing
  behaviour except for the fix at hand; understand, then prove, then improve.
- Before every commit: `gofmt -l internal cmd`, `go vet ./...`,
  `golangci-lint run ./...` (config in the repo root), and
  `go test -race -count=1` for every package touched; for `web/`,
  `npm run lint && npm run typecheck && npm run build`.
- Pull requests are opened as drafts on the working branch; the merge title
  is `Merge pull request #<n> from cploutarchou/triangular-arbitrage-platform`.

## Commands

```bash
go build ./... && go vet ./...
golangci-lint run ./...
go test -race -count=1 ./...                 # database-backed tests skip without the variables below
ARB_TEST_DATABASE_URL="postgres://arb:<password>@localhost:5432/arb_test?sslmode=disable" \
ARB_TEST_DB_DESTRUCTIVE=1 go test -race -count=1 ./internal/storage/...
make test-db                                 # disposable arb_test with the migrations applied via psql
cd web && npm ci && npm run lint && npm run typecheck && npm run build
go test -run xxx -bench 'BenchmarkQuoteCycle50Levels|BenchmarkSizeSearchCycle50Levels' -benchmem ./internal/pricing/
```

CI (`.github/workflows/ci.yml`) runs gofmt, vet, build, the migrations,
`go test -race` against a PostgreSQL service, golangci-lint with gosec,
govulncheck, the console lint/typecheck/build, gitleaks and the Playwright
suite against a real backend. A change is done when all of it is green.

## Engineering standards

- Go: `context.Context` through every request path, small interfaces,
  bounded goroutines, graceful and staged shutdown, structured errors,
  structured logging (`slog`), metrics through `internal/metrics` (atomics
  on hot paths, observed at scrape time).
- Order books: HEALTHY / STALE / CORRUPTED / SYNCING / DISCONNECTED are the
  only states the pricing engine trusts; a crossed or mispriced book is
  CORRUPTED. Book age is checked at qualification and again at fill time.
- Risk: the deterministic risk engine (`internal/risk`) is the only gate;
  breakers have policies in `internal/app/riskpolicy.go`; the paper engine
  revalidates every plan before reserving capital and checks the ledger
  invariants after every settlement.
- Persistence: the outbox is the only write path from the hot path; every
  record ends in exactly one of written, refused or dropped, and the
  counts are exported.
- Migrations: forward-only, `IF [NOT] EXISTS`, matching `.down.sql`, bump
  `LatestMigrationVersion` in `internal/storage/migrations.go`.
- Console: keep the design tokens and components under `web/src/components`,
  dark mode, keyboard access and the contrast check; distinguish loading,
  error and empty states; never render a request failure as "no data".

## Working with the specialists and skills

- Skills (`.claude/skills/<name>/SKILL.md`, loaded by OpenCode from that path): `triangular-arbitrage-platform`
  (the core engine and its reference material), `crypto-arb-platform` (the
  product programme and routing), `scanner-suite`, `venue-connector`,
  `dex-arbitrage`, `saas-billing`, `marketing-site`, `prod-infra`,
  `console-feature`. Load the one that matches the request before starting.
- Specialist agents (`.opencode/agent/<name>.md`): mention one as
  `@<name>` or delegate through the task tool. Route by area: trading math →
  `triangular-engineer`, `quant-researcher`; execution → `execution-simulator`,
  `risk-engineer`; market data → `market-data-engineer`; backend/API →
  `backend-engineer`, `platform-settings-engineer`; database →
  `database-engineer`; console → `ux-designer`, `ui-designer`,
  `frontend-engineer`; Scanner Suite → `screener-engineer`; venues →
  `exchange-connector-engineer`; infra → `infra-sre-engineer`;
  observability → `observability-engineer`; security → `security-engineer`;
  review → `code-reviewer`; tests → `qa-engineer`, `chaos-engineer`.
- Commands (`.opencode/command/`): `/continue-audit`, `/review`,
  `/regression`, `/handover`.
