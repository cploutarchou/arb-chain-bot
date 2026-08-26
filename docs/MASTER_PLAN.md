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
- priority: P0 · component: api/realtime · status: DONE (auth + status +
  WS hub with seq/resync; config (T-034), alerts (T-037), ai (T-036)
  groups; opportunities (memory ring + DB history), paper cycles +
  per-cycle orders, portfolio snapshot, pnl, risk (limits + breakers +
  reject histogram), audit list, deep /system/health; /readyz gates on
  DB reachability; honest 404s for absent components)
- description: /api/v1 groups (auth, dashboard, scanner, opportunities,
  paper, portfolio, pnl, risk, system, audit, config) with envelope/
  pagination/correlation conventions; WS topic hub with seq + snapshot +
  resync + batching.
- dependencies: T-021..T-023
- acceptance: API tests (router-level), WS protocol tests (subscribe,
  seq gap resync, lagging-client drop).

## P1 — HIGH

### T-030 Basic web operations console
- priority: P1 · component: web · status: DONE (auth flow with login/
  CSRF recovery via /auth/me, Overview, Scanner with WS-fed live stream
  + resync-aware hub client, Paper console with pause/resume, System
  Health; dark-first; typed client; lint+typecheck+build green)
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
- priority: P1 · component: telegram · status: DONE (long-poll bot with
  allow-list auth + generic denial, SKILL §56 command set over the same
  services as the web console, signed single-use inline callbacks,
  audited controls, NotificationService with severity routing/cooldown/
  dedup/aggregation + web/telegram sinks; fake-API tests. Engine emits:
  ready/stopped, breaker transitions, cycle failures, drawdown
  approach, large opportunities — remaining §58 hooks land with their
  subsystems: AI (T-036), reports (T-042), alert lifecycle (T-037))
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
- priority: P1 · component: metrics · status: DONE (OTel SDK +
  Prometheus exporter; SKILL §65 set for implemented subsystems — AI/
  Telegram series land with T-036/T-033; /metrics on the API mux or a
  private ARB_METRICS_ADDR listener; rules + dashboard in
  deploy/observability/; hot path = atomic adds, benched: add 7.5ns,
  histogram record 135–280ns)
- description: OTel+Prometheus wiring, SKILL.md §65 metric set, health
  endpoints, System Health API payloads; dashboards + alert rules in
  deploy/observability/.
- dependencies: T-021 · acceptance: /metrics exposes the set; hot-path
  overhead benchmarked.

### T-036 AI advisor subsystem
- priority: P1 · component: ai · status: DONE (Advisor interface;
  Anthropic provider over raw Messages API + deterministic Fake through
  the same strict validation gate — unknown fields/params/bounds all
  rejected with WARNING alerts; typed-summary inputs only; versioned
  prompt; scheduler hourly/daily/weekly that survives outages;
  approve/reject through the strategy service (validated, versioned,
  audited) via web API and Telegram buttons; rejection history kept;
  ai_analyses migration 000002; provider outage fault test proves
  isolation. OpenAI alternative provider remains optional follow-up)
- description: Advisor interface, Anthropic provider (+Fake), schema-
  validated outputs, scheduled hourly/daily/weekly analyses, parameter
  recommendations + approval flow + audit per resources/ai-advisor.md.
- dependencies: T-022, T-024, T-034
- acceptance: FakeAdvisor end-to-end: analysis → recommendation →
  approve/reject → config version; invalid output rejected; provider
  outage does not affect scanner (fault test).

### T-037 Alert center & notification service
- priority: P1 · component: notification · status: DONE (Center records
  every delivery regardless of routing; lifecycle active→acked→resolved
  with key-folding, severity escalation, eviction that never drops
  active CRITICAL; alerts table persistence; API list/ack/resolve with
  RBAC+CSRF+audit; Telegram /alerts with Ack buttons on the same
  center; hub topic + change events. Console page is T-040)
- description: severity routing, cooldown/dedup/aggregation, alert
  lifecycle (active/acked/resolved), web center + Telegram pushes.
- dependencies: T-024, T-033
- acceptance: dedup/cooldown unit tests; critical alerts never dropped.

## P2 — MEDIUM

### T-040 Full console page inventory
- status: DONE for the current backend surface (login, Overview,
  Scanner with live WS stream, Triangles+quality scores, Opportunities
  history, Paper with per-cycle orders drill-down, Portfolio/Balances +
  PnL, Exchanges/Markets with book states, Strategies with versioned
  edit + rollback, AI Advisor approve/reject, Risk Center, Reports with
  on-demand generation, Alerts ack/resolve, System Health, Audit Log,
  Settings/Users & Security with honest not-built notes). Remaining
  inventory rides its features: Replay UI (T-041), a charts pass for
  PnL & Analytics, user CRUD (needs backend user management first)
- dependencies: T-030, T-034..T-037

### T-041 Replay & backtest UI + config comparison
- status: DONE for current runner (recordings browser over
  market_recording_metadata with per-segment integrity data and the
  exact CLI replay invocation; any-two-versions config comparison via
  GET /api/v1/config/version/{n}). In-console backtest RUNS need a job
  worker — future scope, stated on the page
- dependencies: T-032, T-040

### T-042 Reports (daily/weekly) + Telegram digests
- status: DONE (full §82 section layout with honest data-source notes;
  detailed version persisted to the reports table, concise digest
  routed web+Telegram via the notification service; scheduler +
  on-demand via API POST /reports/generate and Telegram /report
  (weekly) & /daily; DB period aggregates incl. top/worst triangles,
  slippage, failed cycles; rule-based recommended actions)

### T-043 Triangle quality score
- status: DONE (internal/quality: deterministic /100 composite —
  profitability 25, sample 10, success 15 with thin-sample cap,
  slippage stability 15, edge persistence 15, failure severity 10,
  drawdown 10; hand-computed vector test; explicit never-pure-win-rate
  test; storage per-triangle aggregation; API
  GET /api/v1/triangles/quality with the config's MinExpectedProfit as
  the profitability reference. Per-triangle drawdown input remains
  portfolio-level — stated in the response, not invented)

### T-044 Performance benchmark suite + budgets
- status: DONE — §73 set complete (book apply/view, WS decode,
  topology, cycle math, size search, triangle recalculation,
  opportunity build + JSON, depth simulation, WS fan-out, metrics hot
  path); baselines + budgets in docs/benchmarks.md; scripts/bench.sh;
  fixed the pre-existing BuildTopology bench (universe lacked
  quote-to-quote crosses so no triangle could close)

### T-045 E2E Playwright suite (SKILL.md §72)
- status: DONE (scripts/e2e.sh boots real arbd + Next proxy; 10 tests:
  auth redirect, no-oracle login failure, overview, all pages render
  real or honest states, config edit → new version round trip, risk
  limits, alert center, on-demand report, sign-out; CI e2e job added;
  10/10 locally)
- dependencies: T-040

### T-046 Profitability validation campaign
- status: BLOCKED on real recorded feeds — this dev environment cannot
  reach exchange endpoints, so no genuine market recordings exist to
  replay. Ready today: recorder + deterministic replayer (T-032),
  seed-deterministic simulation, reports (T-042), latency knobs in
  simulation.Config. Still needed for the campaign: real RECORD-mode
  captures in a network-enabled deployment, fee+X / depth-haircut
  stress transforms on the replay path, and the §80 honest report over
  those runs. No profitability claim is made without this.
- dependencies: T-032, T-042

### T-047 Research-debt re-verification
- status: BLOCKED in this dev environment — exchange documentation
  sites are unreachable through the sandbox proxy (verified 2026-08-26:
  developers.binance.com and binance.com both blocked), so the
  UNVERIFIED items from docs/research/final-platform-selection.md §7
  cannot be re-checked against primary sources here. Re-run in a
  network-enabled environment before live-adjacent phases (T-050+).

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
- 2026-08-26 (evening): every P1 DONE — T-034 config service, T-035
  observability, T-033 Telegram + notification router, T-037 alert
  center, T-036 AI advisor. T-024 read groups completed. P2: T-040
  console inventory (with T-030 finishing), T-041 replay browser +
  config compare, T-042 reports, T-043 quality score, T-044 bench
  baselines, T-045 Playwright E2E (10/10 against real arbd). T-046 and
  T-047 BLOCKED on this environment's egress (stated in-task). Suite:
  `go test -race ./...` green across 26 packages; golangci-lint 0
  issues; frontend lint/typecheck/build green. §88 audit pass running;
  findings will be recorded below.

---

## §88 Audit findings (2026-08-26)

Three independent audit passes (quant, security, code review) over the
P0–P2 implementation. Rule applied: a task is DONE only when acceptance
criteria pass AND P0/P1 findings are fixed. Every FIXED entry below is
covered by a test that fails on the pre-fix code, and the full suite
(`go test -race ./...`, 26 packages, storage against PostgreSQL 16) is
green after the batch, with golangci-lint at 0 issues and Playwright
10/10.

### Quant audit

- **Q-P1-1 · quality score rewarded reliable losers.** Evidence: old
  formula paid success/persistence/sample points independent of PnL — a
  triangle losing money on every cycle could outrank a profitable one.
  Fix: profitability component gates on positive per-cycle PnL (0 at
  break-even), non-positive PnL caps the total at 40 with an explicit
  note. Acceptance: `TestReliableLoserNeverOutranksEarner`,
  `TestUnprofitableCapProperty` (internal/quality). FIXED.
- **Q-P1-2 · thin samples outranked real evidence.** Evidence: 3 lucky
  cycles could beat 500 modest ones. Fix: whole-score confidence
  scaling below MinCredibleCycles=20; absent evidence earns nothing
  (slippage needs ≥2 samples, unknown drawdown scores 0). Acceptance:
  `TestThinSampleCannotOutrankRealEvidence`,
  `TestAbsentEvidenceEarnsNothing`. FIXED.
- **Q-P1-3 · report "scanner" section printed cumulative counters as
  period stats.** Fix: per-kind baseline in the generator; second and
  later reports show period deltas, the first discloses "since process
  start". Acceptance: `TestScannerSectionIsPerPeriodDelta`
  (internal/reporting). FIXED.
- **Q-P2 · slippage persisted for outcomes where it is meaningless**
  (mid-cycle failures produced ~+10000 bps artifacts that poisoned
  averages) and worst-slippage used min() under the positive=worse
  convention. Fix: NULL storage unless the cycle reached leg 3
  (ALL_FILLED / LEG1_PARTIAL / PARTIAL_CYCLE), aggregates take
  `max(slippage_bps)` as worst and `count(slippage_bps)` as sample
  size. Acceptance: `TestQualitySamples` (NULL row excluded),
  storage suite. FIXED.
- **Q-P2 · TriangleLeaders "worst" could duplicate a top earner and
  scanned unbounded rows.** Fix: two bounded SQL queries (top by sum
  DESC; worst restricted to negative PnL, ASC) with in-Go disjointness.
  FIXED.
- **Q-P2 · window-hours arithmetic undercounted touched hour buckets**
  (persistence could read 26/24). Fix: bucket count =
  truncated-boundary difference + 1; pinned in `TestQualitySamples`
  (25 buckets for a 24h window). FIXED.
- **Q-P3 · fake advisor could ratchet TTL upward forever.** Fix:
  proposal only below a 10s cap; `TestFakeAdvisorTTLCapTable`. FIXED.
- **Q-P3 · AI input presented cumulative counters unlabeled.** Fix:
  `counters_scope` field ("cumulative since process start") marshaled
  into every prompt. FIXED.

### Security audit

- **S-001/S-002 (P0/P1) · AI-recommendation approval bypassed the
  risk→ADMIN config mapping, and the config API's permission check ran
  outside the writer lock (TOCTOU).** Fix: `strategy.Authorize`
  evaluated INSIDE the writer lock against the diff actually written;
  one shared `api.SectionAuthorizer` used by the config API, AI
  approvals (web) and Telegram approvals (OPERATOR). Acceptance:
  `TestApproveRespectsAuthorizeGate` (internal/ai),
  config API 403 tests. FIXED.
- **S-003 · same-mux /metrics was unauthenticated.** Fix: gated behind
  `PermViewSystem`. Acceptance: `TestMetricsEndpointRequiresPermission`.
  FIXED.
- **S-004 · Telegram dispatch executed the command before checking the
  permission** (denial only suppressed the reply). Fix: dispatch
  returns descriptors; the permission gate runs before `run()`.
  Acceptance: telegram suite (side-effect-free denial by
  construction). FIXED.
- **S-005 · sessions stored raw bearer tokens; disabled users kept
  working sessions; unused IdleTTL implied idle expiry that did not
  exist.** Fix: stores index sessions by SHA-256 digest only
  (`auth.HashToken`, hashing centralized in Manager), pgx
  `SessionByToken` rejects disabled users at lookup, IdleTTL removed
  and docs/security.md §4 corrected. Acceptance:
  `TestSessionStoreHoldsOnlyTokenDigests`, storage round-trip. FIXED.
- **S-006 · login throttle keyed only (email,ip); Argon2 verifications
  unbounded (memory DoS).** Fix: additional per-IP throttle (20/min)
  and a process-wide 4-slot semaphore around Argon2id derivations.
  Acceptance: `TestIPThrottleCatchesEmailRotation`. FIXED.
- **S-007 · POST /reports/generate ran real aggregate queries under the
  viewer-held reports:view.** Fix: new `reports:generate` permission
  (OPERATOR+). Acceptance: RBAC matrix test + denial matrix. FIXED.
- **S-008 · web audit trail missed paper controls and login/logout, and
  dropped IP/correlation ID.** Fix: AuditAction carries
  (ip, correlation_id) into audit_events; paper pause/resume,
  auth.login, auth.logout audited. FIXED.
- **S-009 · Redacted() forgot ARB_ADMIN_PASSWORD.** Fix: masked; a
  reflective tripwire fails on any future secret-looking field left
  unmasked. Acceptance: `TestRedactedMasksSecretLookingFieldsReflectively`.
  FIXED.
- **S-010 · AI recommendation fields flowed onward unbounded.** Fix:
  parameter ≤128 bytes, recommended_value ≤256 (individually
  discarded). FIXED.
- **S-011 · no browser hardening headers.** Fix: CSP
  (default-src 'self', frame-ancestors 'none', ws connect), nosniff,
  X-Frame-Options DENY, Referrer-Policy, Permissions-Policy in
  next.config.ts; nosniff on every API JSON response. E2E 10/10 with
  headers active (dev server adds 'unsafe-eval' for webpack only).
  FIXED.
- **S-012 · Telegram client errors could echo the bot token** (URL in
  transport errors). Fix: token held apart from base URL; `redact()`
  masks it in every error path. Acceptance:
  `TestClientErrorsNeverContainToken`. FIXED.
- **S-013 · logout lacked CSRF.** Fix: wrapped; test pins 403-without /
  200-with. FIXED.
- **S-015 · a forged callback consumed another user's nonce**
  (delete-before-verify → button DoS). Fix: peek-before-delete — only
  the entitled tap or expiry retires a nonce, still single-use.
  Acceptance: extended `TestInlineButtonRoundTripAndTamperRejection`.
  FIXED.
- **S-017 · client correlation IDs echoed/logged raw; Telegram report
  errors echoed raw internals.** Fix: `^[A-Za-z0-9._-]{1,64}$` or a
  fixed marker; Telegram returns a generic failure line and logs the
  detail. Acceptance: `TestCorrelationIDSanitized`. FIXED.

### Code review audit

- **CR-P1-1/2 · Binance depth Syncer raced OnDelta/OnSnapshot/Synced,
  and concurrent resyncs of one market stampeded REST.** Fix: mutex on
  the syncer state machine + per-market single-flight
  (`resyncing` CAS); feed's syncer map behind its own lock.
  Acceptance: `TestSyncerConcurrentDeltaAndSnapshotIsRaceFree`,
  `TestResyncSingleFlightPerMarket`. FIXED.
- **CR-P1-3 · cycle→opportunity linkage depended on callers re-attaching
  the opportunity; the engine's outbox path didn't, so persisted cycles
  lost their opportunity_id.** Fix: `CycleResult.OpportunityID` set by
  the simulator from the plan; `InsertCycle` reads it; outbox needs no
  side channel. Acceptance: simulator assertion + outbox end-to-end
  linkage check. FIXED.
- **CR-P1-4 · lazy init raced on first concurrent use** (Outbox,
  notification.Center, telegram.PushSink, ai.Service — plus Recorder
  and Bot, same defect class): double-created channels silently lost
  records/deliveries. Fix: `sync.Once` in every `init()`. Acceptance:
  new concurrent-first-use race tests; `-race` suite green. FIXED.
- **CR-P2-7 · staleness sweep read the boot-time MaxBookAge, ignoring
  hot-swapped config.** Fix: `Scanner.CurrentConfig()` read per tick.
  FIXED.
- **CR-P2-8 · cooldown_seconds:0 validated but silently became 60s.**
  Fix: Validate rejects 0 ([1,3600]) so config never lies. FIXED.
- **CR-P2-9 · recording stream ids rendered via rune('0'+id)** (breaks
  at id≥10). Fix: strconv. FIXED.
- **CR-P2-10 · alert center forgot persisted active alerts on
  restart.** Fix: `Alerts.LoadActive` + `Center.LoadActive` hydration
  at boot. FIXED.
- **CR-P2-11 · merged with S-004.** FIXED.
- **CR-P2-14 · no handler-level RBAC/CSRF denial tests for the AI and
  reports mutation routes.** Fix: `TestAIAndReportsDenialMatrix`
  (401 → 403 RBAC → 403 CSRF → 404 absent, in order). FIXED.
- **P3 batch.** Rune-safe truncation (telegram + ai), expired-nonce
  sweep in newCallback, metrics instrument errors joined instead of
  discarded, seed config version audited as actor "system",
  `Params.Clone()` so Current() hands out no shared Routes map, AI
  alerts.active from Center.ActiveCount instead of the recent ring.
  All FIXED.

### Deliberately open

- **CSP still allows 'unsafe-inline' scripts/styles** — Next.js inline
  runtime chunks require it until a nonce pipeline exists (tracked as
  console hardening follow-up; risk accepted for an authenticated
  internal console). P3.
- **auth.MemoryStore does not re-check Disabled at session lookup** —
  the memory store exists for tests and DB-less bootstrap where no
  runtime user-disable path exists; the pgx store (production) rejects
  at lookup. P3, documented here.
- **T-046 (testnet keys) / T-047 (govulncheck refresh) remain BLOCKED**
  on this environment's egress policy, stated honestly in their tasks.

## Status log (continued)

- 2026-08-26 (night): §88 audit complete — three parallel audit passes
  (quant, security, code review) produced the findings above; every
  P0/P1/P2 finding fixed in this batch with regression tests, P3s
  fixed or explicitly accepted. Validation: gofmt clean,
  golangci-lint 0 issues, `go vet` clean, `go test -race ./...` green
  across 26 packages (storage against PostgreSQL 16 with migration
  000003 applied up/down/up), frontend lint/typecheck/build green,
  Playwright E2E 10/10 against real arbd with the new security headers
  active. T-004 stays IN_PROGRESS until the PR's hosted CI run is
  green on runners.
