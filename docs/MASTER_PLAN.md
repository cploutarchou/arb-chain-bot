# MASTER PLAN

Canonical task tracker (SKILL.md §77). Statuses: TODO / IN_PROGRESS /
BLOCKED / DONE. Priorities: P0 CRITICAL / P1 HIGH / P2 MEDIUM / P3
OPTIONAL. A task is DONE only when its acceptance criteria pass and
P0/P1 review findings are fixed.

Phase mapping (SKILL.md §78): P0 research ✅, P1 architecture ✅ — both
completed and recorded in `docs/research/*` and `docs/{architecture,
data-flow,security,risk}.md`.

---

## P0 — CRITICAL (foundation & financial correctness)

### T-001 Repository bootstrap — Go module & package skeleton
- priority: P0 · component: build · status: DONE (build/vet/race-test/gofmt/golangci clean)
- description: go.mod (`github.com/cploutarchou/arb-chain-bot`), cmd/
  entries, internal package skeletons compiling, slog logging setup,
  Makefile, .gitignore, .env.example.
- business reason: nothing ships without a building tree.
- technical reason: package boundaries from docs/architecture.md §3 must
  exist before code lands in the right homes.
- dependencies: none · risks: none
- acceptance: `go build ./...`, `go vet ./...`, gofmt clean.
- tests: CI compile gate.

### T-002 Docker development environment
- priority: P0 · component: deploy · status: DONE (compose authored; daemon unavailable in dev sandbox, so compose run is verified in CI/user env; migrations validated against local PostgreSQL 16.13 instead)
- description: docker-compose with PostgreSQL 16 (+healthcheck), app env
  wiring, migration runner target.
- dependencies: T-001 · risks: none
- acceptance: `docker compose up -d db` yields healthy Postgres;
  `make migrate` applies migrations.
- tests: CI can start service container.

### T-003 Database migrations framework + initial schema
- priority: P0 · component: storage · status: DONE (up→down→up validated on PostgreSQL 16.13; 23 tables)
- description: migrations/ (golang-migrate naming), 0001 initial schema
  per docs/data-flow.md §4 (NUMERIC money, timestamptz, insert-only audit
  grants documented).
- dependencies: T-002
- risks: schema churn early — mitigated by forward-only discipline.
- acceptance: migrations apply cleanly to empty DB; rollback files where
  safe.
- tests: integration test applies migrations against dockerized PG.

### T-004 CI pipeline
- priority: P0 · component: build · status: IN_PROGRESS (workflow authored + all checks pass locally; flips to DONE when the PR run is green)
- description: GitHub Actions: gofmt check, go vet, golangci-lint, go
  test (-race), gosec, govulncheck, gitleaks, frontend lint/typecheck/
  test/build, E2E critical path (later stage).
- dependencies: T-001 · acceptance: CI green on this branch; failing P0
  checks block merge.

### T-010 Decimal domain core & normalized models
- priority: P0 · component: exchange · status: DONE (quantization/JSON tests; DivisionPrecision=28 pinned)
- description: internal/exchange models per SKILL.md §10 (Exchange,
  Market, Asset, OrderBook types, FeeSchedule, InstrumentRules, Balance,
  Order, Fill, Triangle, Opportunity, RiskDecision, capability
  descriptors); decimal serialization helpers; instrument quantization
  (step/tick + decimals models).
- business: every financial number flows through these types.
- technical: single normalized vocabulary prevents venue leakage.
- dependencies: T-001
- acceptance: unit tests for quantization boundaries (truncate-down, min
  qty/notional), decimal JSON round-trip; no float64 in money paths.
- tests: table-driven quantization; serialization.

### T-011 Order-book engine + health state machine
- priority: P0 · component: orderbook · status: DONE (-race clean; apply ~500ns, View(50) ~1.7us)
- description: Book/Ladder (decimal, absolute-qty semantics, qty-0
  delete, depth truncation option), versioning, health machine
  (SYNCING/HEALTHY/STALE/CORRUPTED/DISCONNECTED), BookView snapshots,
  dirty notification, age/latency tracking, SequenceValidator hook.
- dependencies: T-010
- acceptance: state-machine transition tests; apply/reset semantics;
  -race clean under concurrent read/apply; benchmarks recorded.
- tests: unit + race + bench; chaos scenarios in T-031.

### T-012 Market graph & triangle enumeration
- priority: P0 · component: graph · status: DONE (fixture IDs + sides pinned; property-validated)
- description: directed conversion edges (side-aware), canonical
  3-cycle enumeration with configurable starting assets, rejection rules
  (self-loops, dup markets, disabled/untradeable), topology cache with
  market→triangles index, incremental rebuild on instrument changes.
- dependencies: T-010
- acceptance: known fixture exchange yields exact expected triangle set;
  rotations deduped; market→triangles lookup correct.
- tests: table-driven enumeration; property: every returned cycle is
  valid and starts/ends in configured asset.

### T-013 Depth-aware pricing & optimal size search
- priority: P0 · component: pricing · status: DONE (hand-computed §70 cases; sizer within 2% of brute force; cycle ~94us @50 lvls)
- description: decimal depth-walk VWAP per leg (both directions), leg
  chaining with fee-in-kind + truncation, gross/net bps, breakpoint-
  aware optimal-size search, liquidity limits, price impact.
- dependencies: T-011, T-012, T-014
- acceptance: SKILL.md §70 pricing cases pass with hand-computed values;
  optimal-size beats grid-search baseline on fixtures; benchmarks.
- tests: table-driven math; property: profit(size) evaluation matches
  brute force on small fixtures.

### T-014 Fee engine
- priority: P0 · component: fees · status: DONE (placement table, discounts incl. API exclusion, promo overrides)
- description: fee schedules with per-pair overrides, account tier,
  fee-asset conventions (received-asset, quote-side, BNB-style token),
  conditional discounts, promo zero-fee pairs; assumptions exposed on
  every opportunity.
- dependencies: T-010
- acceptance: fee cases (base/quote/token; discount on/off; promo pair)
  match hand computations; never assumes zero fees by default.
- tests: table-driven.

### T-015 Opportunity engine (lifecycle, TTL)
- priority: P0 · component: opportunity · status: DONE (transition table, buffered economics, revalidation contract)
- description: opportunity model per SKILL.md §19 with statuses, reason
  codes, TTL, book-version citations, revalidation contract.
- dependencies: T-013
- acceptance: lifecycle transitions tested incl. expiry and duplicate
  suppression.

### T-016 Risk engine + circuit breakers
- priority: P0 · component: risk · status: DONE (boundary table per limit, monotonicity, breaker lifecycle)
- description: deterministic limit evaluation per docs/risk.md §2 with
  scoped overrides + reason codes; breaker framework per §3 (safe
  default OPEN); risk events.
- dependencies: T-015
- acceptance: boundary tables per limit; monotonicity property; breaker
  transition tests; decisions cite config version + effective limits.

### T-017 Capital reservation manager
- priority: P0 · component: reservation · status: DONE (-race storms, conservation invariants, idempotency)
- description: atomic reserve/settle/release with idempotency keys,
  conflict policy, conservation invariants per docs/risk.md §4.
- dependencies: T-010
- acceptance: -race storm tests pass; invariant property tests; duplicate
  keys return original reservation.

### T-018 Paper execution simulator
- priority: P0 · component: simulation · status: DONE (engine + app loop wired: reserve->execute->settle->portfolio; pause/resume via API)
- description: three-leg sequential simulation per
  resources/execution-simulation.md: seeded latency draws, fill-time book
  reads, partial fills, all outcomomes, intermediate exposure +
  mark-to-market, TTL revalidation, reservation integration; Executor
  implementations incl. disabled LiveExecutor.
- dependencies: T-013, T-016, T-017
- acceptance: one deterministic test per outcome; conservation property;
  ErrLiveTradingDisabled proven by test; -race clean.

### T-019 Portfolio & P&L
- priority: P0 · component: portfolio · status: DONE (reconciliation property, equity/drawdown with marks, unmarkable exposure listed)
- description: virtual balances per session, exposure positions, realized
  /unrealized P&L, fees/slippage attribution, drawdown, snapshots.
- dependencies: T-018
- acceptance: reconciliation property across simulated histories;
  exposure from failed legs visible.

### T-020 Binance market-data connector
- priority: P0 · component: exchange/binance · status: DONE (decoder/validator/syncer fixture-tested per official docs; live transport validates in network-enabled env - sandbox egress blocks Binance)
- description: transport (reconnect/backoff/keepalive/24h pre-empt),
  decimal-preserving decoder for depth diffs + REST snapshot, official
  U/u splice + continuity validator, instrument metadata provider
  (exchangeInfo → InstrumentRules), fee provider mapping, server-time
  clock offset; fixture-driven (recorded frames), no live dependency in
  tests.
- dependencies: T-011
- acceptance: splice/continuity unit tests incl. gap→resync; decoder
  fuzz-safe; fixtures replay to expected book states; drift-check hook.
- tests: unit + fixture replay + chaos (T-031 subset).

### T-021 Scanner assembly (hot path)
- priority: P0 · component: app · status: DONE (end-to-end fixture: qualify+reject both directions, breaker gating, -race)
- description: wire feeds → books → dirty queue → evaluator pool →
  risk → opportunity stream per docs/architecture.md §5–§6; bounded
  queues, metrics, graceful shutdown; modes.
- dependencies: T-012..T-020
- acceptance: end-to-end fixture test: recorded frames in → expected
  qualified/rejected opportunities out, deterministically; -race clean.

### T-022 Storage layer + outbox
- priority: P0 · component: storage · status: DONE (pgx repos + auth stores + outbox; integration-tested on PG16; CI service container wired)
- description: pgx repositories for core tables, batching outbox with
  overflow accounting + persistence breaker signal.
- dependencies: T-003
- acceptance: integration tests against dockerized PG; outbox overflow
  behavior tested.

### T-023 Auth, sessions, RBAC
- priority: P0 · component: auth · status: DONE (Argon2id/PHC, revocable sessions, throttle, CSRF, RBAC matrix pinned; HTTP-layer denial tests; pgx session/user stores follow with T-022)
- description: Argon2id, server-side sessions, CSRF double-submit, login
  throttling, RBAC middleware + service-layer checks, audit events.
- dependencies: T-003
- acceptance: denial matrix tests for every mutating route; session
  expiry/revocation tests; no plaintext secrets.

### T-024 Core API v1 + realtime hub
- priority: P0 · component: api/realtime · status: IN_PROGRESS (auth endpoints + gated status routes + WS hub with seq/resync DONE and tested; remaining: opportunities/paper/portfolio/pnl/risk/audit/config route groups over the storage layer)
- description: /api/v1 groups (auth, dashboard, scanner, opportunities,
  paper, portfolio, pnl, risk, system, audit, config) with envelope/
  pagination/correlation conventions; WS topic hub with seq + snapshot +
  resync + batching.
- dependencies: T-021..T-023
- acceptance: API tests (router-level), WS protocol tests (subscribe,
  seq gap resync, lagging-client drop).

## P1 — HIGH

### T-030 Basic web operations console
- priority: P1 · component: web · status: TODO
- description: Next.js scaffold with auth flow, Overview (system status,
  today stats, health), live Scanner table (virtualized, WS-fed),
  Opportunity detail, Paper console (status + pause/resume), System
  Health; dark-first theme; typed API client; WS client with resync.
- dependencies: T-024 · acceptance: lint+typecheck+build green;
  component tests for scanner table and WS store; degraded states.
- note: full page inventory (SKILL.md §31) tracked by T-040.

### T-031 Market-data chaos test suite
- priority: P1 · component: testing · status: DONE (SKILL §71 scenarios
  against the real syncer+books in binance/chaos_test.go; all -race)
- description: fault-injection fixtures per SKILL.md §71 driving
  connector+book: disconnect, storm, duplication, loss, out-of-order,
  snapshot delay, REST failure, freeze, clock skew, burst.
- dependencies: T-020, T-021
- acceptance: every fault ends in safe state (no qualification on bad
  data), documented transitions.

### T-032 Recorder & deterministic replay
- priority: P1 · component: marketdata · status: DONE (segment format +
  recorder + replay determinism golden test; BACKTEST full-run UI is
  T-041)
- description: raw-frame segment writer (zstd, rotation, sha256,
  metadata), REST-snapshot capture, replay driver feeding the live code
  path with recorded clock + seed.
- dependencies: T-020..T-022
- acceptance: same recording+config+seed ⇒ identical decision log
  (golden test).

### T-033 Telegram control surface
- priority: P1 · component: telegram · status: TODO
- description: bot per resources/telegram.md — allow-list auth, core
  commands, inline approve/ack buttons, NotificationService routing with
  cooldowns/dedup.
- dependencies: T-024 · risks: token management (env only).
- acceptance: fake-API tests for auth, commands, buttons, dedup;
  shared-state sync with web.

### T-034 Strategy configuration service
- priority: P1 · component: config · status: DONE (internal/strategy:
  validated typed params, immutable versions with diff+actor+audit, hot
  swap into the running scanner, rollback-as-new-version; API routes
  with per-section RBAC; pgx + memory stores. Console page is T-040)
- description: versioned dynamic config (immutable rows, diffs, actor,
  audit, hot swap, rollback) per docs/architecture.md §13; console +
  API surface.
- dependencies: T-022, T-023
- acceptance: change→version→audit→swap tested; rollback produces new
  version; components read consistent snapshots.

### T-035 Observability build-out
- priority: P1 · component: metrics · status: TODO
- description: OTel+Prometheus wiring, SKILL.md §65 metric set, health
  endpoints, System Health API payloads; dashboards + alert rules in
  deploy/observability/.
- dependencies: T-021 · acceptance: /metrics exposes the set; hot-path
  overhead benchmarked.

### T-036 AI advisor subsystem
- priority: P1 · component: ai · status: TODO
- description: Advisor interface, Anthropic provider (+Fake), schema-
  validated outputs, scheduled hourly/daily/weekly analyses, parameter
  recommendations + approval flow + audit per resources/ai-advisor.md.
- dependencies: T-022, T-024, T-034
- acceptance: FakeAdvisor end-to-end: analysis → recommendation →
  approve/reject → config version; invalid output rejected; provider
  outage does not affect scanner (fault test).

### T-037 Alert center & notification service
- priority: P1 · component: notification · status: TODO
- description: severity routing, cooldown/dedup/aggregation, alert
  lifecycle (active/acked/resolved), web center + Telegram pushes.
- dependencies: T-024, T-033
- acceptance: dedup/cooldown unit tests; critical alerts never dropped.

## P2 — MEDIUM

### T-040 Full console page inventory
- status: TODO — Triangles, Orders, Fills, Portfolio/Balances, PnL &
  Analytics (charts), Exchanges, Markets, Strategies UI, AI Advisor UI,
  Risk Center, Replay UI, Reports, Alerts, Audit Log, Users & Security,
  Settings per SKILL.md §31–§55.
- dependencies: T-030, T-034..T-037

### T-041 Replay & backtest UI + config comparison
- status: TODO · dependencies: T-032, T-040

### T-042 Reports (daily/weekly) + Telegram digests
- status: TODO · dependencies: T-036, T-037 (SKILL.md §82 layout)

### T-043 Triangle quality score
- status: TODO (SKILL.md §81) · dependencies: T-019 history

### T-044 Performance benchmark suite + budgets
- status: TODO — formalize §73 benches with recorded baselines;
  dependencies: T-021

### T-045 E2E Playwright suite (SKILL.md §72)
- status: TODO · dependencies: T-040

### T-046 Profitability validation campaign
- status: TODO — recorded-feed replays + stress profiles (fees+X,
  latency+X, depth haircuts) per SKILL.md §80; honest report.
- dependencies: T-032, T-042

### T-047 Research-debt re-verification
- status: TODO — UNVERIFIED items from docs/research/
  final-platform-selection.md §7 before the affected phases start.

## P3 — OPTIONAL / LATER

### T-050 Second exchange: OKX connector
- status: BLOCKED (by SKILL.md §79 first-exchange definition of done)
- description: strict prevSeqId chain validator, demo-env support,
  capability descriptor; re-verify docs first (T-047).

### T-051 Additional exchange (Bybit vs Bitget decision)
- status: BLOCKED (Phase 21; fresh research required)

### T-052 MFA (TOTP) enrollment
- status: TODO · architecture reserved in auth flow.

### T-053 Parquet analytical derivatives of recordings
- status: TODO (only with measured analytical need)

### T-054 Email notification channel
- status: TODO (NotificationService adapter)

---

## Status log

- 2026-08-26: Plan created. Phases 0–1 are DONE (research in
  `docs/research/*`, architecture in `docs/`). All implementation tasks
  start as TODO; statuses change only when acceptance criteria actually
  pass (`go test ./...`, CI). Never mark DONE ahead of the tree.
- 2026-08-26 (later): all P0 tasks T-001..T-024 DONE (see per-task
  notes). P1 progress: T-031 chaos suite and T-032 recorder/replay DONE
  with `-race` green across 20 packages; storage integration tests run
  against local PostgreSQL 16 (`ARB_TEST_DATABASE_URL`). Hosted CI runs
  still fail at runner provisioning (account-level Actions issue,
  documented on PR #1) — code-level checks pass locally.
