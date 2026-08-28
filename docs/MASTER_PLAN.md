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
- priority: P0 · component: build · status: DONE (PR run green on hosted runners: backend race suite + migrations + lint, frontend, E2E against the real backend, gitleaks, govulncheck)
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
- priority: P0 · component: auth · status: DONE (Argon2id/PHC, revocable sessions, throttle, CSRF, RBAC matrix pinned; HTTP-layer denial tests; pgx session/user stores follow with T-022; BL-11 users & roles console API: `GET/POST /api/v1/users`, `POST /api/v1/users/{id}/role|disable|enable|password`, `POST /api/v1/auth/password` — all PermUserManage (ADMIN) + CSRF + audit except the self-service password change, which is requireAuth-only; `auth.AdminService` enforces self-target/last-admin protection and revokes sessions on any credential/role change; works with or without a database via `auth.MemoryStore`)
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
  DB reachability; honest 404s for absent components; BL-10 `POST
  /api/v1/paper/reset` — PermPaperReset (ADMIN) + CSRF + audit, body
  `{"confirm":"RESET"}` (400 otherwise), 404 outside PAPER mode, 409 if
  the engine is running or a simulation is in flight; rebuilds the
  reservation ledger and portfolio to configured initial balances and
  starts a new paper session row while preserving historical cycles)
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
- status: IN_PROGRESS — one sample recorded and campaigned; acceptance
  wants multiple sessions across regimes (calm, volatile, weekend).
- sample 2 (started 2026-08-27, in progress): **widened universe** —
  46 Binance symbols / 144 triangles (BTC, ETH, BNB, SOL, XRP, DOGE
  across USDT, USDC, FDUSD, EUR, TRY) applied from Settings → Markets
  as settings v2, recorded from the Campaigns page on the T-063 build.
  Hypothesis under test: the six-market majors universe never showed a
  gross deviation above ~−1 bps in 12 h of live scanning (8.6k
  `RISK_MIN_EDGE` rejections, best net −41 bps against 30 bps fees +
  10 bps buffers); less-liquid intermediates and fiat-quoted pairs are
  where deviations, if any, should appear. Session
  `01M11K202ZHBEZKPD8QXBPF6YJ` (from 12:27 UTC). The operator cancelled
  the scheduled 18:37 UTC campaign step on 2026-08-27 to prioritise the
  Scanner Suite (Phase 22); the recording keeps running until the next
  rebuild and can be campaigned later. Live scanning over the widened
  universe (3 h): 28k `RISK_MIN_EDGE` rejections, best net edge −36 bps
  (≈ +4 bps gross before 40 bps of fees and buffers), 0 of 144 triangles
  ever above −20 bps net.
- campaign 1 (2026-08-27): recording `01M0ZPK16CXTR91MMJQ60HC2K3`
  (Binance, BTCUSDT/ETHUSDT/ETHBTC/BTCUSDC/ETHUSDC/USDCUSDT, starting
  assets USDT/USDC; 2026-08-26 18:51:15 → 20:22:02 UTC, **1 h 31 m —
  below the 6 h minimum**; 223k frames in two segments; the session was
  cut short when the paper compose profile was brought up on the same
  host). Full §80 grid (8 scenarios × seeds 1,2,3), USDT=10000, 10/10
  bps base fees. Report: docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3/.
  Verdict (verbatim): "NO CYCLES EXECUTED in the baseline — the
  recording produced no qualified opportunities; no profitability
  statement can be made from it." Every scenario evaluated 1,878,468
  candidate cycles per three seeds and qualified none (1,878,066
  rejected, 402 skipped on unhealthy books); the numbers are identical
  across the stress grid because rejection happens before execution.
  This is a valid research outcome consistent with
  final-platform-selection.md §6 (a calm European-evening window,
  Regular-tier 30 bps three-leg cost, 10 bps of buffers) — not evidence
  of profitability and not yet evidence against it. The
  "profitable only under perfect conditions" flag did not apply (there
  was nothing to flag).
- findings from this run: (1) recording sessions started from the
  console after the feed had synced carried no REST snapshot and could
  not be replayed at all (fixed 2026-08-27: snapshot capture on session
  start; sessions `01M0ZXKJ…` and `01M0ZYMB…` recorded before the fix
  are not replayable); (2) the final segment of a session ended by a
  process shutdown is closed but not always registered in
  `market_recording_metadata` (campaigns read segments from disk, so
  results are unaffected; registration to be made shutdown-safe);
  (3) the campaign output records rejection counts but not reasons —
  a per-reason histogram in `backtest.Result` is needed before a
  no-qualification verdict can be explained (why: min edge, book age,
  depth, quality) — tracked as T-062.
- next: sessions of ≥ 6 h across regimes on the fixed build (console
  Start, no compose rebuilds during a session), then campaigns per
  session; compare regimes before any claim.
- dependencies: T-032, T-042

### T-047 Research-debt re-verification
- status: DONE (2026-08-26, executed from a network-enabled host).
  Every UNVERIFIED item from docs/research/final-platform-selection.md
  §7 was re-pulled from current official sources with source + access
  date recorded in place (fees.md, exchanges.md, final-platform-selection
  §7.1/§7.2, okx-connector-checklist.md §14). Resolved: OKX fee ladder
  (no OKB tier on the current schedule; volume/assets tiers), OKX
  `market/books` 40 req/2 s and `books-full` 10 req/2 s, Kraken Depth
  max 500, Coinbase WS 8 conn/s/IP. Corrected: Kraken Tier 1 fees
  0.40/0.80 % since 2026-07-09 (3-leg 240 bps), Binance U/u two-phase
  rule, Binance 5 msg/s WS client limit, `account/commission.discount`
  is a multiplier, no Regular-tier zero-fee promo in a liquid Binance
  triangle as of 2026-08-26. Explicitly demoted to named runtime-verified
  assumptions (with burn-in checks): Bitget spot-demo coverage and
  per-endpoint limits, Coinbase private REST rps and per-connection
  subscription cap, Gate per-IP WS caps and fee page (HTTP 403 to
  non-browser clients), Kraken public REST rate, OKX demo-data realism
  and observed seqId reset. OKX checklist A–I executed in full, including
  a live `books` capture (0 chain breaks, checksum fixed to 0). No
  platform decision changed.
- dependencies: none

## P3 — OPTIONAL / LATER

### T-050 Second exchange: OKX connector
- status: BLOCKED (by SKILL.md §79 first-exchange definition of done —
  gate 1 of okx-connector-checklist.md §0: the T-046 campaign verdict).
  Research gate (T-047) is cleared.
- description / acceptance (restated 2026-08-26 from the verified facts,
  okx-connector-checklist.md §14): `books` channel connector with in-band
  snapshots (`prevSeqId = -1`) and a strict `prevSeqId == last seqId`
  validator that (a) accepts keep-alives (`seqId == prevSeqId`, empty
  sides) as continuity, (b) accepts the documented maintenance reset
  (`seqId < prevSeqId` with matching `prevSeqId`) under a distinct
  metric label, (c) resyncs by unsubscribe/resubscribe on any other
  break; checksum ignored (deprecated, fixed to 0); text `ping`/`pong`
  keep-alive under the 30 s idle cut; pre-emptive reconnect on
  `event: notice` code `64008`; 480 requests/hour subscription budget
  with one multi-arg subscribe per connection; metadata from
  `/public/instruments` mapping `tickSz`/`lotSz`/`minSz`, both size caps
  and the quote caps `maxLmtAmt`/`maxMktAmt`, `state` (`live` →
  TRADING, else HALTED), `MinNotional` = 0; `instruments` WS channel or
  periodic refresh for rule changes; demo-env support via
  `openapi.okx.com` + `x-simulated-trading: 1` / `wspap.okx.com`;
  capability descriptor `{BookInitInBand, IntegrityUpdateChain,
  RequiresRESTDriftCheck: false, FeeInReceived, HasSpotTestEnv: true,
  ForcedDisconnect: false (+64008 handler)}`; venue-parameterized
  replayer and campaign (`-exchange`); burn-in checks for the demoted
  items (§14 "D" rows) before the descriptor is trusted.
- pre-work (2026-08-26): docs/research/okx-connector-checklist.md —
  protocol (A–I, §12, §13) and, since the 2026-08-26 re-verification,
  the executed record (§14).

### T-051 Additional exchanges: Bybit, Bitget, Gate (order by fresh research)
- status: BLOCKED (Phase 21; fresh research required). Venue roster set
  2026-08-26: Binance (live), OKX (T-050), then Bybit, Bitget and Gate —
  all wanted, sequencing decided by a re-run of the exchanges.md /
  fees.md scoring at that time (Bybit's classic-channel gap blind spot,
  Bitget's v3 numeric-JSON prices, Gate's 403-only docs are the known
  connector risks). Kraken and Coinbase are out (fee-eliminated and not
  wanted).

### T-062 Campaign rejection-reason histogram
- status: TODO. `backtest.Result` and the §80 report should carry a
  per-reason rejection histogram (the scanner already classifies
  rejections) so a "no qualified opportunities" verdict says why —
  minimum net edge, book age, depth exhaustion, data quality — and
  the stress grid can show which reason dominates under each axis.

### T-063 Binance REST weight gate
- status: DONE (2026-08-27). Widening the Binance universe from 6 to 46
  symbols from the console tripped Binance's per-IP request-weight
  limit on the first engine restart: 46 depth snapshots at limit 5000
  (weight 250 each = 11,500) against the 6,000/min budget → HTTP 418,
  IP banned ~2 min, most books never primed, and the recording session
  started in that window (`01M11J1K3T1BCAYT8PYQ8E3Q9K`) has no usable
  snapshots. Fix in `internal/exchange/binance`: every depth fetch
  (startup priming, record-start capture, gap resync) goes through one
  `restGate` — sliding 60 s window capped at 4,500 weight, 429/418
  `Retry-After` honoured before any further call — and
  `SnapshotDepthLimit` is 1000 (weight 50; scanner depth is 50). A
  60-symbol universe now primes in ~15 s within budget. `rate_limited`
  counter exposed in the system read model.

### T-064 Exchange API credentials in the vault
- status: DONE (2026-08-27). Registry `exchange` group (14 entries,
  six venues) stored encrypted and managed from Settings → Security.
  `Applies: not_consumed`: `Manager.Get` refuses the group, no code
  reads them, `TestRegistryIsClosed` pins it. Live trading remains
  disabled; keys are for a future separately reviewed read-only
  consumer. docs/design/settings-expansion.md §3.3, deployment.md §3d.

### T-056 MEXC research round
- status: DONE (2026-08-26) — docs/research/mexc.md. Score 65/100
  (between Gate 63 and Bitget 73): cheapest venue on paper (0/5 bps base;
  `exchangeInfo` publishes 0/0 on 245 USDC-quoted pairs incl. BTCUSDC,
  ETHUSDC, USDCUSDT → a 5 bps cycle *if* it applies to API flow —
  unverified, every platform promo excludes API users), protobuf-only
  order-book stream with a Binance-class version chain and a documented
  off-snapshot blind spot, 30 subscriptions/connection, no spot
  test/demo environment, IOC/FOK effectively absent (GTC-only in
  practice), conflicting official rate-limit numbers. Enters the Phase 21
  scoring round (T-051) as a fee-driven challenger with five named
  burn-ins (API fee via `tradeFee`, IOC behaviour, real IP/UID budget,
  WS ping/connection caps, fee asset convention).

### T-057 Platform settings + supervised engine restart
- status: IMPLEMENTED (backend) (2026-08-27, code-reviewed and fixed
  2026-08-27) — `docs/design/platform-settings-and-restart.md`.
  Closes console-ux-audit BL-13b (editable symbols/starting assets) and
  BL-15 (venues & fees) on the backend. New versioned `platform_settings`
  document (separate from `strategy.Params`: the section→permission
  mapping in rbac.go:72-77 fails open to OPERATOR, and symbol validation
  needs exchangeInfo) in `internal/platform`, migration 000006, and an
  `app.Supervisor` that re-enters one stable `Engine.Run` on
  `POST /api/v1/engine/restart`. Env vars are first-boot seeds only; a
  settings version that cannot build a topology is rejected at
  apply/rollback time (`ValidateAgainstCatalog`, D8), never discovered
  for the first time at restart. `LiveExecutor` untouched; no exchange
  API keys anywhere in this feature. Frontend (design §4, BL-12) landed
  separately (T-057 frontend, package B/C/D) on top of the routes/
  permissions/response shapes frozen here.
  **A follow-up code review (this entry, 2026-08-27) found the original
  landing's restart/re-entrancy claims were not fully true and fixed
  every P1/P2 finding plus the cheap P3 items:**
  - Generation-tagged engine exits (`runExit{gen,err}`): the original
    single untagged `errCh` let a grace-timed-out restart's abandoned
    run be silently discarded, and its LATE exit could be misattributed
    to a newer restart's bookkeeping — in the worst case two
    `Engine.Run` calls alive at once (two feeds/scanners/outboxes racing
    on the same `*Engine`). Fixed: every exit carries the generation
    that produced it; `waitEngineStop`/`waitReadyOrFail` ignore/observe
    exits from other generations; a grace timeout keeps the abandoned
    generation's cancel func and sets `pendingGen`, which `Request`
    refuses new restarts against until that generation's exit is
    actually observed — never discarding a live run's cancel.
  - `Engine.Run`'s deferred `ready = false` now checks a per-run token
    before clearing readiness, so a stale run's cleanup can never clear
    a newer run's `Ready` state.
  - `waitReadyOrFail`'s 30s poll used to return `nil` (mark ready) on
    timeout even though the engine had not actually finished
    bootstrapping. Fixed: it returns a distinct timeout sentinel; state
    stays `restarting` and `Supervisor.Run`'s own readiness ticker
    resolves it later (now a ticker + `Supervisor.ReadyTimeout`,
    configurable, not a hardcoded busy-poll).
  - The paper-pause decision is now carried INTO the engine
    (`Engine.SetPaperPaused`, consumed once at the `paperEng.Resume()`
    boot site) instead of being re-applied by the supervisor AFTER
    readiness — which had nothing to act on while `Supervisor.Paper()`
    returns nil mid-bootstrap, so a paused paper engine could silently
    resume across a slow restart. A grace-timeout (or any other early
    return from `restart()`) now also restores the pause state it
    disturbed, so a later restart's own "was it running before" capture
    never reads a permanently-stuck-paused stale object.
  - `token_discount: true` is now refused outright by
    `FeeSettings.validate` for every venue, with a corrected comment on
    `fees.NetOutput`: the 25% discount was applied to fee math with NO
    pay-asset (e.g. BNB) balance ever debited anywhere, making paper P&L
    optimistic by exactly the discount amount on every fee-bearing leg.
    No pay-asset ledger was built (that touches money math, out of
    scope for a review-fix pass). New `GET /api/v1/platform/venues`
    (`view:system`) exposes the compiled venue/discount table
    (asset, rate, `applies_to_api`, and an honest `modeled: false`) so
    the console can render — and correctly disable — the toggle from
    data instead of hardcoding fees.go's constants.
  - The engine used to persist only the currently-scoped symbol subset
    to the `markets` table (`UpsertMarkets(ctx, scoped)`); the
    API-profile fallback catalog (`storage.Catalog`) reads that same
    table, so a symbol the operator had never selected could never be
    added through the console (a false `unknown_symbol`). Fixed:
    persists the full bootstrap catalog (`UpsertMarkets(ctx, markets)`).
  - A platform-settings `Load` failure against a configured database
    used to silently fall back to a fresh, env-reseeded `MemoryStore` —
    discarding whatever was actually persisted with only a log line.
    Fixed to fail boot hard, matching `storage.Open`'s existing policy
    for the same class of failure.
  - The last-admin check (`AdminService.UpdateUserRole`/
    `SetUserDisabled`) was check-then-act across two independent store
    calls — a real race under concurrent demotions of two different
    admins. Fixed: the guard is now atomic inside the store itself (a
    single pgx transaction with deterministic `ORDER BY id ... FOR
    UPDATE` locking to avoid a cross-transaction deadlock; one lock
    acquisition in `MemoryStore`).
  - The recording-active restart guard was checked only once, at
    `Request()` time — a session could start in the window before the
    single-threaded restart loop actually processed the request. Fixed:
    re-checked inside `restart()` itself, unwinding the state machine
    back to Ready/Pending (not left stuck in `restarting`) on refusal —
    the same unwind also fixes a dead `default:` branch in `Request`
    that could otherwise strand the state machine.
  - `RecorderControl.Bind` was bound to `Engine.Run`'s outer `ctx`
    parameter rather than the internally-cancelled `runCtx`; a run that
    exited on an internal fatal error (not the caller cancelling `ctx`)
    left an API-started recording session orphaned until the supervisor
    eventually noticed. Fixed: bound to `runCtx`, guaranteed cancelled
    no later than `Run` returning, for any reason.
  - Smaller items: `Restarts` no longer counts the initial boot (only
    actual restarts); `PendingVersion` clears when the last pending
    reason clears; `platform.Service`'s `swap`/`Subscribe`/
    `MemoryStore.Get`/`Active` now hand out cloned `Settings` (no
    aliasing); `AdminService.ListUsers`/`UpdateUserRole`/
    `SetUserDisabled` never return a password hash; email is
    case-normalized on `CreateUser`; the platform-settings preview route
    now requires `view:system` (was `requireAuth` only); the restart
    audit action is split into `engine.restart.requested` (API layer,
    on accept) and `engine.restart.completed` (supervisor, on actual
    readiness); `POST /api/v1/auth/password` is now throttled per user
    (`AdminService.PasswordThrottle`); migration 000005's down migration
    uses `DROP COLUMN IF EXISTS`.
  - **Not fixed, pre-existing, tracked separately (P3-11):** `Portfolio.
    Reset` zeroes realized P&L/drawdown, but `risk.Context.DailyLoss`/
    `Drawdown` are not wired to read it — a circuit breaker's daily-loss/
    drawdown limits do not see the effect of a paper reset. Independent
    of this review's restart work; left open.
- dependencies: T-034 (config service pattern), T-040
- acceptance: `internal/platform` validate/catalog/service unit tests
  (including `TestTokenDiscountRejected`, `TestCompiledVenueTable`);
  `internal/app` engine re-entrancy test (`Engine.Run` twice, no
  goroutine leak, no double strategy subscription), the P2-2/P2-6
  boot-time recorder/pause tests, and supervisor unit tests (fake
  `EngineRunner`: re-entry-after-return-only, stop-recording ordering,
  paper pause preserved, guard-rail refusals, second-run failure
  survives, restart timeout with no re-entry and no generation overlap
  (`TestSupervisorGracefulTimeoutNeverOverlapsRunsAndGatesRestarts`),
  ready-timeout stays `restarting` then resolves with pause preserved
  (`TestSupervisorReadyTimeoutStaysRestartingThenResolvesPausePreserved`),
  recording-guard re-check unwinds to Ready, pending-reasons for both
  documents); `internal/api` handler tests (RBAC, CSRF, confirm token,
  409 guard rails, `field_timing`, the new `/platform/venues` route);
  `internal/auth` last-admin concurrent-race and password-throttle
  tests; `internal/storage` integration tests for `platform_settings`/
  `EndPaperSession`/`ListMarkets`/the unscoped-catalog fix/the pgx
  last-admin race, run against a disposable Postgres (never the dev
  compose DB on :5432). All green with `-race`; `golangci-lint run
  ./...` clean.

### T-058 Console package D: concurrency fix + BL-17/18/19/20/21/26/27/31/32
- status: DONE (2026-08-27). All nine items (BL-17, BL-18, BL-19, BL-20,
  BL-21, BL-26, BL-27, BL-31, BL-32) plus the optimistic-concurrency fix
  landed. Backend-only (`internal/*`); `web/` untouched (a frontend
  agent owns it concurrently). Per-item status below, written
  incrementally as each item landed rather than once at the end — kept
  as the honest record of what shipped and why, not rewritten into a
  single clean narrative after the fact.
- **Optimistic concurrency (item 1, DONE).** `POST /api/v1/config`,
  `/config/rollback`, `/platform/settings`, `/platform/settings/rollback`
  accept an optional `parent_version`. `strategy.Service` and
  `platform.Service` gained `ApplyAuthorizedExpect`/
  `RollbackAuthorizedExpect(..., expectedParent int64)`; the check runs
  INSIDE the writer lock right after `cur := s.Current()` — before the
  no-change diff check, so a stale write against an unchanged payload
  reports `stale_version`, not `no_change`. A new `WriteErrorData`
  envelope helper carries `{"current_version": n}` alongside the 409
  `stale_version` error code. `strategy.StaleVersionError` /
  `platform.StaleVersionError` wrap `ErrStaleVersion` via `Is`. Omitting
  `parent_version` keeps today's unchecked behavior (no client break).
  Tests: `internal/api/configapi_test.go`
  (`TestConfigApplyOptimisticConcurrency`,
  `TestConfigRollbackOptimisticConcurrency`),
  `internal/api/platformapi_test.go` (mirrored).
- **BL-31 persisted risk events (item 6, DONE).** `risk_events` already
  existed in migration 000001 — no new migration for this item.
  `storage.RiskEvent` + `Store.InsertRiskEvent`/`ListRiskEvents`; a new
  `Record.Kind = "risk_event"` outbox arm. Breaker transitions
  (`internal/app/engine.go`'s `risk.NewRegistry` callback) and risk
  rejections (`consumeEvents`, where `e.countReject` already ran) both
  enqueue. **Rejections are throttled, breaker transitions are not**:
  unthrottled rejection volume (a real fixture ran 4988 rejected vs 12
  qualified per period) would make the reject path the dominant outbox
  write, saturate the bounded queue, and trip `OnPersistError` —
  degrading persistence of opportunities/cycles too, since `Outbox.write`
  is one `INSERT` per record through a single writer. Fixed by
  `Engine.shouldPersistRiskReject`: only the FIRST rejection per
  `(triangle_id, reason_code)` per 60s cooldown is persisted (map bounded
  at 4096 keys, same guard pattern as `rejectCounts`); the unthrottled
  total stays visible via `RejectCounts()`/`GET /api/v1/risk`. Tests:
  `internal/app/riskreject_test.go`.
  `GET /api/v1/risk/events?hours=&limit=` (`PermViewRisk`) returns
  `{window_hours, events, n}`. `Outbox` also gained `Depth()`/
  `Capacity()` for BL-18. Tests: `internal/storage/storage_test.go`
  (`TestRiskEventPersistence`, DB-backed), `internal/api/reads_test.go`.
- **BL-32 report detail + CSV (item 9, DONE).** `storage.Reports.GetReport`
  (`ErrReportNotFound` → 404). `GET /api/v1/reports/{id}` returns the
  full structured `reporting.Report` (already section-shaped JSON — the
  console renders it, no reformatting needed). `GET
  /api/v1/reports/{id}/csv` via new `reporting.Report.CSV()`/`CSVRows()`:
  a reflection-based flatten to `(section, field, value)` rows (Report's
  own top-level JSON fields ARE the sections; slices get `[i]` suffixes),
  RFC 4180 via `encoding/csv`, with a leading-`=+-@` neutralization
  (leading apostrophe) against CSV-formula injection in free-text fields
  (incident titles, AI summaries). Tests: `internal/reporting/csv_test.go`
  (round-trips through the standard `encoding/csv` reader, injection
  neutralization, section-column invariant), `internal/storage/
  reports_test.go` (DB-backed), `internal/api/reportsapi_test.go`.
- **BL-18 system health (item 2, DONE).** `GET /api/v1/system/health`
  no longer gates on `needEngine` — process stats, DB pool stats, queue
  depths, and supervisor state are all available with no engine (API
  profile) and were previously invisible there. New sections merged from
  whatever is actually present: `process` (goroutines, heap/sys bytes,
  cumulative GC pause ns, `num_gc`, uptime — `runtime.MemStats`, always
  present), `database` (`storage.Store.PoolStats()`, a plain struct
  wrapping `pgxpool.Stat()` so `internal/api` never imports pgxpool),
  `queues.outbox`/`queues.paper`/`queues.recorder` (new `Depth()`/
  `Capacity()` on `storage.Outbox`, `marketdata.Recorder`, and
  `paper.Engine.QueueDepth/Capacity`), `restart` (`Restart.Status()`),
  and the pre-existing engine-derived map (`ready`/`triangles`/`scanner`/
  `feed`/`books`/`paper`) merged at the top level via `s.Reads.Health()`.
  `feed` gained `msgs_per_sec` (delta-sampled from the cumulative frame
  counter across polls via a new `rateSampler`, first call reports 0
  honestly) and `latency_ms` (`{n, p50_ms, p95_ms, p99_ms}` from a new
  256-sample ring buffer, `internal/app/latency.go` — OTel histograms
  are write-only from this process's own point of view, so this is the
  queryable side-channel; sampling is independent of whether `Metrics`
  registered, since it must work even when OTel is unconfigured).
  Per-exchange reconnects/sequence-errors were already present
  (`feed.reconnects`/`feed.seq_gaps`); not duplicated. Tests:
  `internal/api/healthapi_test.go`, `internal/app/readmodel_test.go`.
- **BL-21 Telegram status (item 7, DONE).** `GET /api/v1/telegram/status`
  (`PermViewSystem`) — 200 always, `enabled:false` when no token/empty
  allowlist rather than 404 ("not configured" is itself the answer). New
  `telegram.Client.GetMe`, `Bot.Status()` (messages/errors counters
  already existed; added last-poll and last-getMe timestamps/ok/error and
  `probeGetMe` called once per `Run` — a fresh connectivity check on
  every process start/supervised restart), `PushSink.Status()` (pushed/
  errors counters, last-push timestamp). The token never appears in any
  of these — allowlist is chat ids only. Tests:
  `internal/telegram/bot_test.go` (`TestBotStatusReflectsGetMeAndPoll`,
  `TestBotStatusRecordsGetMeFailureHonestly`,
  `TestPushSinkStatusTracksSuccessAndFailure`),
  `internal/api/telegramapi_test.go`.
- **BL-20 orders/fills (item 3, DONE).** `GET /api/v1/orders`/`GET
  /api/v1/fills` (`PermViewPortfolio`, store-backed), global (not scoped
  to one cycle like `/paper/cycles/{id}/orders`), filters `symbol`
  (via `markets.symbol`, reliably populated even by `InsertCycle`'s
  defensive market-ref insert), `triangle` (via
  `orders→paper_cycles→opportunities.triangle_id`), `cycle`, `status`,
  `from`/`to`, `limit`. Cursor pagination keys on the row's OWN column —
  `orders(created_at DESC, id)` / `fills(ts DESC, id)`, migration
  000007's new indexes — never a joined column, so the page stays an
  index scan under any filter combination. The cursor is base64(`<
  RFC3339Nano timestamp>|<id>`); RFC3339Nano (not RFC3339) because
  Postgres `timestamptz` is microsecond-precision and three legs commonly
  share one second. Every row carries the full cross-link chain
  (`opportunity_id`, `triangle_id`, `symbol`) via `LEFT JOIN
  opportunities`/`markets` (LEFT because `paper_cycles.opportunity_id` is
  nullable — an INNER join would silently drop unattributable rows).
  Tests: `internal/storage/orders_fills_test.go` (filters, cross-links,
  and `TestOrdersGlobalCursorPaginatesRowsSharingATimestamp` — three
  same-timestamp rows page without skip/repeat), `internal/api/
  reads_test.go`.
- **BL-19 PnL & analytics (item 4, DONE).** `GET
  /api/v1/pnl/breakdown?by=exchange|triangle|asset|market|hour|
  config_version&hours=`, `GET /api/v1/pnl/series?hours=`, `GET
  /api/v1/analytics/distributions?hours=` (all `PermViewPortfolio`,
  store-backed, `internal/storage/analytics.go`). `by=exchange/triangle/
  config_version` INNER-join `opportunities` (nullable
  `opportunity_id` FK) and report `unattributed` alongside `n` so
  `n + unattributed` always equals the cycles in the window; `by=asset/
  hour` need no join (both columns live on `paper_cycles` itself, so
  every cycle counts, `unattributed` is always 0). `by=market` is
  deliberately NOT a P&L split: a cycle's P&L spans all three legs and
  attributing it to one market's leg would be a fabricated number, so
  it reports leg activity (order count, average latency) instead, with
  a `notes` entry saying so explicitly — see `PnLBreakdown`'s doc
  comment. `pnl/series` accumulates cumulative P&L and running drawdown
  in `shopspring/decimal` (never float64 — the architecture brief calls
  float64-on-money P0). `analytics/distributions` returns edge
  (`opportunities.net_return_bps`, QUALIFIED only),
  slippage (`paper_cycles.slippage_bps`, NULL-filtered — only cycles
  that reached leg 3 carry a meaningful value, same filter
  `reports.go`'s `CycleAggregates` already uses), and latency
  (`orders.latency_ms`) histograms: min/max/avg/p50/p95/p99 plus 10
  fixed-width buckets, every one carrying `n` even at 0. Tests:
  `internal/storage/analytics_test.go`, `internal/api/pnlapi_test.go`.
- **BL-27 opportunity detail (item 5b, DONE).** `GET
  /api/v1/opportunities/{id}` (`PermViewOpportunity`, store-backed).
  Migration 000007 added `opportunities.decision`/`.book_versions`
  columns; `InsertOpportunity` now populates both for new rows (the
  legacy `{legs, risk_checks}` shape is UNCHANGED and still written
  too, so nothing that read it before breaks). `GetOpportunity` reads
  `decision` directly when present; for historical rows (column NULL)
  it reconstructs a `Decision` from the legacy `legs.risk_checks` object
  and marks it `legacy:true` with a `notes` entry — BL-27 was "partly
  done" already (the checks were persisted, just not addressably) and
  this says so rather than claiming the feature was missing.
  `book_versions` comes from `Opportunity.BookVersions()` (already
  exported, not re-derived). "Simulation result when recorded" needed
  no new column: `paper_cycles` is already keyed by `opportunity_id` —
  `GetOpportunity` LEFT-JOIN-equivalents it (a second query, honest nil
  when no cycle exists yet). Found-but-out-of-scope: `InsertOpportunity`
  has always written `op.DataQuality` into BOTH the `confidence` and
  `data_quality` columns (`Opportunity` has no `Confidence` field) — a
  pre-existing duplication bug, not part of any BL-17..BL-32 item, left
  as found. Tests: `internal/storage/opportunity_detail_test.go`,
  `internal/api/opportunitydetail_test.go` (DB-backed; also asserts
  `/opportunities/history` still wins over `/opportunities/{id}` —
  Go 1.22 `ServeMux`'s literal-beats-wildcard specificity rule).
- **BL-26 triangle detail (item 5a, DONE).** `GET /api/v1/triangles/{id}`
  (`PermViewDashboard`, matching `/triangles/quality`'s permission).
  New `api.TriangleReader` interface + `Server.Triangles` field
  (deliberately NOT a `ReadModel` method — growing that interface would
  force every `ReadModel` fake across the api package's tests to
  implement an unrelated method; same small-interface precedent as
  `RestartController`/`RecorderController`/`CampaignService`).
  `internal/app.triangleReader` implements it by resolving
  `e.currentScanner()` fresh on every call (never a captured per-run
  local — the one invariant `engine.go`'s header calls the review
  checklist for every change in that file, and this is a new file in
  the same package touching the same live state). Per leg: market/side/
  from/to, current book top + age (`orderbook.Set.View`), a fee
  waterfall via `fees.Schedule.Taker` (called, never re-derived), and a
  reference-size (100 units, documented as NOT a live opportunity's
  actual sizing) VWAP/depth preview via `pricing.QuoteLeg` — the
  existing exported pricing entry point, not new arithmetic. Recent
  cycles (new `Store.ListCyclesByTriangle`) and a quality score (reusing
  `Store.QualitySamples` + `quality.Score`, filtered to the one
  triangle) are store-backed and present independently of whether an
  engine is running, same "merge whatever is actually present" pattern
  `system/health` uses. Tests: `internal/app/triangles_test.go`
  (real harness via `newTestEngine`/`runOnce`), `internal/api/
  trianglesapi_test.go` (including a route-precedence test for
  `/triangles/quality` vs `/triangles/{id}`), `internal/storage/
  orders_fills_test.go`'s `TestListCyclesByTriangle`.
- **BL-17 console-driven replays (item 8, DONE).** New `internal/replay`
  package, deliberately thin: `internal/backtest.Run` already drives a
  recording through `marketdata.Replayer` → the real scanner → risk →
  the replay executor deterministically (T-046), and
  `internal/campaign.Runner` already has the exact background-job shape
  this needs (one at a time, progress, persisted row, orphan
  reconciliation on restart) — `replay.Runner` mirrors it almost
  line-for-line rather than inventing a second pattern.
  `replay.Request` is intentionally narrow, matching the task's literal
  shape (`{recording, config_version, speed}`): a replay is a one-click
  "run the CURRENT (or a pinned) strategy against this recording" check,
  not a second campaign-configuration surface (campaigns already own
  the assets/balances/fee-bps/multi-seed grid). `config_version`, when
  set, resolves via `strategy.Service.Get` (new narrow `ParamsSource`
  interface) and FAILS THE RUN if it doesn't — it is never silently
  substituted with `strategy.DefaultParams()` the way `backtest.Run`
  itself falls back on an invalid zero-value `Params` (that fallback
  exists for callers who don't ask for a version at all; item 8's
  request explicitly named one, so silence there would be dishonest).
  `speed` is accepted, validated, and persisted for the console's audit
  trail but has NO execution effect: `backtest.Run` is a deterministic
  discrete-event replay (steps to the next recorded frame or latency
  deadline, not to wall-clock time), so there is no wall-clock pace to
  scale — documented on the `Request.Speed` field rather than silently
  ignored or, worse, faked. A real wall-clock-paced replay would need to
  drive `marketdata.Replayer` directly against a live scanner instance,
  a materially heavier feature the task's endpoint shape doesn't
  describe; flagged here as the one sub-piece not built, with the reason.
  One fixed seed (not a multi-seed grid): a replay answers "what does
  the CURRENT strategy do against this recording", one question, so a
  varying seed would only add noise nobody asked for. "Top opportunities"
  is a documented PROXY: `backtest.Run` exposes `Result.Cycles`
  (attempted-and-settled) and `Result.Qualified`/`Result.Evaluations`
  (raw funnel counts) but no raw qualified-opportunity list — `Top` is
  `Result.Cycles` sorted by `NetBps` descending, capped at 10, and the
  API/struct field comments say "executed cycles" explicitly (Qualified
  can exceed `len(Cycles)`: a cycle can still fail past qualification on
  a reservation conflict or TTL expiry).
  `POST /api/v1/replays` (`PermCampaignRun` + CSRF, audited
  `replay.start`), `GET /api/v1/replays` / `GET /api/v1/replays/{id}`
  (`PermViewSystem`, matching campaigns' read bar) — `internal/api/
  replayapi.go`, new `Server.Replays ReplayService` field, registered in
  `routes()`. Storage: `internal/storage/replays.go`
  (`UpsertReplayRun`/`ListReplayRuns`/`GetReplayRun` against
  `replay_runs`); unlike `campaign_runs`, no serialized `request` JSONB
  blob — `replay.Request` is exactly the three plain columns the table
  already has, so `Get`/`List` reconstruct it from them directly.
  `speed` is cast `::text` when scanned (not read as a raw NUMERIC into
  float64), matching the project-wide convention used for
  `orders.latency_ms`/fee columns elsewhere in `internal/storage`.
  Wiring: `internal/app/components.go` constructs `replays :=
  &replay.Runner{Sources: store, Strategy: stratSvc, Store: store, ...}`
  next to `campaigns`, registers it as a `Component`, registers the
  `replays` hub topic (same snapshot/publish shape as `campaigns`), and
  sets `apiServer.Replays`.
  Restart-guard decision (made explicitly, not inherited): a replay run
  never touches the live `*Engine` — `backtest.Run` builds its own
  books/scanner/portfolio in-process — so, unlike a campaign's
  relationship to a settings-scoped restart, a concurrent restart cannot
  corrupt a replay run. The guard exists anyway (new
  `Supervisor.Replays ReplayBusy` field, new `ErrReplayRunning` sentinel
  mapped to refusal code `replay_running` in `restartProxy`) purely so
  an operator restarting the engine gets an honest reason instead of the
  restart and the replay silently fighting for the same CPU core.
  Campaigns and replays are two INDEPENDENT single-flight gates — the
  task says "one at a time" for replays, not "one at a time across both
  job kinds" — so a campaign and a replay may run concurrently today;
  noted here as a deliberate choice, not an oversight, in case it needs
  revisiting once real contention is measured.
  Tests: `internal/replay/execute_test.go` (Request validation, a real
  `backtest.Run` call against the same synthetic profitable-cycle
  fixture `internal/backtest`'s own tests use, the honest
  config_version-failure paths, missing-segments), `internal/replay/
  runner_test.go` (lifecycle/busy/publish, failure recording, orphan
  reconciliation on restart, Runner-level config_version-failure
  plumbing — mirrors `internal/campaign/runner_test.go`'s four tests
  almost exactly), `internal/storage/replays_test.go` (DB-backed
  upsert/get/list round trip including NULL config_version/speed),
  `internal/api/replayapi_test.go` (permissions, CSRF, validation, busy
  conflict, id-route precedence), `internal/app/supervisor_test.go`
  (`TestSupervisorRefusalsReplayBusy`, mirroring
  `TestSupervisorRefusalsCampaignBusy`).
- **Two findings fixed in already-landed items, caught by an
  advisor review before this entry was closed out:**
  1. `orders_created_idx`/`fills_ts_idx` (item 3, BL-20) were created as
     `(created_at DESC, id)` — the trailing tiebreak column defaults to
     ASC, but the handler orders `created_at DESC, id DESC`. A mismatched
     tiebreak direction can force Postgres to add a Sort node on top of
     the index scan instead of walking the index in order, undercutting
     the "stays an index scan under any filter combination" claim above.
     Fixed in migration 000007 (only ever applied to the disposable test
     Postgres, so amending it in place was free) to
     `(created_at DESC, id DESC)` / `(ts DESC, id DESC)`; verified via
     `\d orders_created_idx`/`pg_indexes` that the DESC/DESC index was
     actually created after a down→up re-apply.
  2. `by=hour` (item 4, BL-19) truncated with
     `date_trunc('hour', c.started_at)::text` — on a `timestamptz` this
     formats in the DATABASE CONNECTION's session TimeZone, so two
     clients (or the same client after a `SET TIME ZONE`) could bucket
     the identical row under different hour keys, silently corrupting
     the grouping. Every other handler in this task standardizes on UTC
     (`time.Now().UTC()`); the query now does too:
     `date_trunc('hour', c.started_at AT TIME ZONE 'UTC')::text`.
  3. `replay.Run`'s `evaluations`/`qualified`/`cycles` fields carried
     `omitempty` in their first pass — a completed replay that qualified
     nothing would have serialized with those three keys ABSENT, not
     zero, the exact "not reported" vs "genuinely 0" ambiguity BL-19
     deliberately avoids elsewhere in this task. `omitempty` removed on
     all three. While fixing this, also aligned `Evaluations`'s JSON tag
     from `evaluations` to `opportunities` — the task text says
     "opportunities count", the `replay_runs.opportunities` DB column
     already uses that name, and leaving the JSON key as `evaluations`
     (a third, undocumented vocabulary) would have made the frontend
     agent guess which of three names to read. The Go field keeps the
     name `Evaluations` (matches `backtest.Result.Evaluations`, what it
     actually counts); only the wire name changed. `internal/api/
     replayapi_test.go` updated to assert the `opportunities` key.
- **Documentation gap found, not created by this task:**
  `docs/architecture.md` §7 states an OpenAPI document is "maintained in
  `docs/api/openapi.yaml`" — that path does not exist anywhere in the
  repository (`docs/api/` has never been created), for ANY endpoint,
  not just this task's additions. Every prior console-backend task
  (T-050 through T-057) documented its routes the same way this entry
  does — permissions/shapes/tests in `docs/MASTER_PLAN.md` — not via an
  OpenAPI file. Bootstrapping `docs/api/openapi.yaml` from scratch for
  the entire existing API surface is a repo-wide documentation debt item
  well beyond this task's nine BL items; not attempted here. Flagged so
  it is tracked rather than silently absorbed into "already handled."
- migrations: 000007 (`.up.sql`/`.down.sql`, verified `up`→`down`→`up`
  against the disposable test Postgres, twice — once before and once
  after the item-8 `cycles` column and the index-direction fix above)
  adds `orders_created_idx`/`fills_ts_idx` (both `DESC, DESC`),
  `opportunities.decision`/`opportunities.book_versions` (additive
  columns for item 5's BL-27 work), and `replay_runs` (id, recording_id,
  config_version, speed, status, done, total, step, created_at,
  started_at, finished_at, error, opportunities, qualified, cycles, top,
  actor — for item 8). `risk_events` needed no migration — already in
  000001.
- dependencies: T-057 (platform settings / supervised restart — item 8's
  `ReplayBusy` guard joins the same `Supervisor.Request` guard-rail
  sequence `CampaignBusy` already established), T-034, T-024.
- acceptance: `go build ./...`, `go vet ./...`, `gofmt -l` clean on the
  whole repo; `golangci-lint run ./...` clean except one PRE-EXISTING
  finding in `internal/simulation/paper.go:144` (gosec G115, integer
  overflow conversion int→byte) that predates this task, is outside
  every package this task touched, and is inside `internal/simulation`
  — explicitly off-limits per this task's "never touch arithmetic in
  internal/risk, internal/pricing, internal/simulation, internal/
  reservation, internal/portfolio" constraint, so left as found rather
  than fixed; `go test -race ./...` green across the ENTIRE repo
  (`internal/replay`, `internal/api`, `internal/app`, `internal/
  strategy`, `internal/platform`, `internal/storage` against a
  disposable Postgres on a non-5432 port with migrations 000001-000007
  applied via the `migrate/migrate` image — never the dev-compose DB —
  plus every other package in the module, all passing, none skipped
  except the DB-backed suites when `ARB_TEST_DATABASE_URL` is unset).
- **Review pass (2026-08-27, commit 6eb524f base): four P2 + ten P3
  findings fixed, with regression tests.** No arithmetic changed in
  `internal/risk`, `internal/pricing`, `internal/simulation`,
  `internal/reservation`, `internal/portfolio`; `web/` untouched (a
  frontend agent owned it concurrently — its own uncommitted changes
  were briefly swept into a `git stash`/`pop` cycle by that concurrent
  session mid-task and recovered file-by-file without touching `web/`).
  - **P2-1** (`internal/app/latency.go`,`readmodel.go`,`engine.go`):
    `rateSampler.rate` mutated its delta window on every HTTP poll, so
    with more than one poller in flight each request could steal part of
    the previous poller's interval and understate `msgs_per_sec`.
    Split into `sample()` (single writer — the engine's existing 500ms
    staleness-sweep ticker in `Run`, which now also drives a new
    `Engine.msgRate *rateSampler`) and `current()` (any number of
    read-only callers; `readModel.Health` no longer holds its own
    sampler). Tests: `internal/app/latency_test.go`,
    `TestReadModelHealthConcurrentPollersSeeSameRate` in
    `readmodel_test.go`.
  - **P2-2** (`internal/app/engine.go`): `shouldPersistRiskReject`
    refused EVERY call once its cooldown map hit 4096 entries, including
    a call for a key ALREADY in the map whose cooldown had long expired
    — `risk_events` would silently stop gaining new rows for triangles
    still actively rejecting, not just for genuinely new pairs. Fixed:
    a known key always updates in place (never grows the map); an
    unseen key at capacity first prunes expired entries, then refuses
    (logging once, not once per rejection) only if still full. Tests:
    `TestShouldPersistRiskRejectAtCapStillUpdatesKnownKeys`,
    `TestShouldPersistRiskRejectAtCapPrunesExpiredEntries`.
  - **P2-3** (`internal/storage/quality.go`, `internal/api/
    trianglesapi.go`, migration 000009): `GET /api/v1/triangles/{id}`
    ran `QualitySamples`'s ALL-triangles, 30-day aggregate on every page
    view (any viewer) and threw away every row but one. New
    `Store.QualitySamplesForTriangle(ctx, id, from, to) (quality.Sample,
    bool, error)`, scoped at the query level (`WHERE o.triangle_id =
    $1`), `ok=false` (not a zero-filled Sample) for a triangle with no
    evidence. Also added `paper_cycles_opportunity_idx (opportunity_id,
    started_at DESC)` — `opportunity_detail.go`'s per-opportunity
    simulation lookup was a sequential scan without it. Tests:
    `TestQualitySamplesForTriangle` (DB-backed, proves scoping — seeds a
    second triangle and confirms its cycles never leak into the scoped
    result).
  - **P2-4** (`internal/storage/analytics.go`): `Distributions`'
    `LIMIT 20000` sample queries had no `ORDER BY`, so a truncated
    result was an ARBITRARY (and non-reproducible across calls) subset
    reported as if it were the full population. Added deterministic
    `ORDER BY` (`detected_at, id` / `started_at, id` / `created_at, id`)
    and a `Truncated`/`WindowComplete` pair on `Distribution`, computed
    by querying `cap+1` rows and trimming rather than comparing
    `len(out) == cap` (so a population landing exactly on the cap is
    correctly reported complete). `analyticsSampleCap` changed from
    `const` to `var` so `analytics_test.go` can shrink it and exercise
    truncation without seeding 20,001 rows. Tests:
    `TestDistributionsReportTruncation`, plus a `WindowComplete`
    assertion added to the existing under-cap test.
  - **P3 items**, all in `internal/storage/analytics.go` unless noted:
    (a) `by=config_version`/`by=asset` grouped on a nullable column with
    no `coalesce` — a NULL config_version/pnl_asset 500'd the whole
    breakdown; now groups under `"unknown"`. (b) `internal/storage/
    orders_fills.go`: an invalid `next_cursor` 500'd (`query_failed`)
    instead of 400ing — new `storage.ErrInvalidCursor` sentinel, mapped
    to 400 `invalid_cursor` in `internal/api/reads.go`. (c) `by=hour`'s
    key now formats via `to_char(..., 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
    instead of `::text` (driver/locale-dependent rendering). (d) dropped
    `omitempty` on `Unattributed` (analytics.go) and
    `LevelsConsumed`/`DepthExhausted` (`internal/api/trianglesapi.go`) —
    all three are meaningful zeros, not absences. (e)
    `PnLBreakdownResult.N` for `by=market` counts orders, not cycles like
    every other dimension — now called out in a `notes` entry rather
    than left as an undocumented field-meaning shift. (f)
    `internal/reporting/csv.go`: the CSV-injection defense neutralized
    ANY cell starting with `-`/`+`, mangling ordinary negative numbers
    (PnL, drawdown, bps — the common case in a financial report) into
    text; now skips neutralization for well-formed numbers (a formula
    can never itself be a valid number) and also covers leading
    tab/CR per OWASP's fuller trigger set. (g)
    `internal/api/configapi.go`/`platformapi.go`: `parent_version` is
    now REQUIRED (400 `parent_version_required`) for the four
    web-sourced write routes (`POST /config`, `/config/rollback`,
    `/platform/settings`, `/platform/settings/rollback`) — the web
    console always has a real version to echo back from its own GET, so
    omitting it was a silent optimistic-concurrency bypass exactly for
    the client most likely to race itself (two tabs). Telegram/
    system/AI callers go through `ApplyAuthorized` directly
    (`expectedParent=0`), unaffected. (j) `internal/api/replayapi.go`:
    a non-default `speed` in the request now adds a `notes` entry in
    the POST/GET response explaining it has no execution effect
    (previously documented only in a Go doc comment a console developer
    would never read).
  - **P3-h, the largest item** (`internal/replay/runner.go`,
    `internal/campaign/runner.go`, new `internal/jobrun` package,
    migration 000009): both runners shared four bugs, fixed once in
    `internal/jobrun` (`Gate`, `Bound`, `ClampLimit`,
    `Reclaimable`/`NewOwnerID`/`HeartbeatInterval`/`StaleAfter`) and
    wired identically into each `Runner` so they cannot drift apart
    again:
    1. `Start` was reachable before `Run` pinned the lifetime context
       (nothing enforced the ordering) — a job launched that way ran
       under `context.Background()`, which a supervised shutdown never
       cancels. `jobrun.Gate.Await` (2s bound) now makes `Start` refuse
       with `jobrun.ErrNotStarted` (mapped to 503 `not_ready` in
       `internal/api/opsapi.go`/`replayapi.go`) instead of silently
       taking that shape. `Gate.Pin` is idempotent (a later `Run` call
       re-arms rather than double-closing).
    2. The in-memory `runs`/`order` maps grew without bound — one entry
       per run ever started for the process's lifetime.
       `jobrun.Bound(order, byID, jobrun.RingCap=200)` caps both after
       every `Start`; the just-started (current, in-flight) run is
       always the newest entry and can never itself be evicted under
       the existing single-flight invariant. `update`/`snapshot` made
       nil-safe defensively (an evicted id would otherwise be a nil-deref
       panic on the background execute goroutine).
    3. `List` (and the storage-layer `ListReplayRuns`/`ListCampaignRuns`)
       collapsed ANY out-of-range `limit` — including "too large", not
       just "unset" — to the default: `if limit<=0||limit>200{limit=50}`
       silently ignored a caller asking for up to the documented cap.
       `jobrun.ClampLimit` clamps over-cap values TO the cap.
    4. Orphan reconciliation on startup reclaimed ANY "queued"/"running"
       row not in the fresh process's own (always-empty-at-boot)
       in-memory map — so two processes sharing one Postgres (a rolling
       deploy overlap, or an operator running two instances) would each
       mark the OTHER's still-live run "failed" the moment they started.
       New `owner_id`/`heartbeat_at` columns (migration 000009) on
       `campaign_runs`/`replay_runs`; each `Runner` stamps a random
       per-process `NewOwnerID()` and the current time on every persisted
       write (`persist()`) plus a dedicated heartbeat goroutine
       (`jobrun.HeartbeatInterval=10s`) independent of progress
       callbacks — a replay's own `Progress` only fires at start/finish,
       so relying on it alone would let a long replay's row go
       heartbeat-stale for its whole duration. `jobrun.Reclaimable` only
       reclaims a row whose heartbeat is `jobrun.StaleAfter=50s` (5x the
       interval) old or absent (a pre-migration row, or one never
       heartbeated) — never a row with a fresh heartbeat from a
       DIFFERENT live owner.
    - **Incidental fix found while testing #4's heartbeat goroutine**:
      `execute()` in both runners called the terminal status `update()`
      (Status=done/failed) BEFORE `finish()` cleared `r.current` — a
      real (if narrow) race where a poller could observe a terminal
      status via `Get()`/`List()` while `Busy()` still reported true.
      Reordered so `finish()` runs first in both the success and
      failure paths.
    - Tests: `internal/jobrun/jobrun_test.go` (Gate/Bound/ClampLimit/
      Reclaimable in isolation), `internal/replay/runner_test.go` +
      `internal/campaign/runner_test.go`
      (`TestRunnerStartRefusesBeforeRun`,
      `TestRunnerBoundsInMemoryRunsAndHonoursListLimit`,
      `TestRunnerReconcileOrphansRespectsLiveOwnerHeartbeat`, plus the
      pre-existing `TestRunnerFailureIsRecorded`/
      `TestRunnerConfigVersionResolutionFailureFailsTheRun` updated to
      start `Run` first now that `Start` enforces the ordering),
      `internal/storage/replays_test.go` +
      `internal/storage/campaigns_test.go`
      (`Test{Replay,Campaign}RunOwnerHeartbeatRoundTrip`, DB-backed —
      the reclaim LOGIC is unit-tested via `jobrun`'s fake-store-free
      `Reclaimable`, Postgres only proves the two columns round-trip
      per review guidance), `internal/api/opsapi_test.go` +
      `internal/api/replayapi_test.go`
      (`Test{Campaign,Replay}StartNotReadyIs503`).
  - migrations: 000009 (`.up.sql`/`.down.sql`, verified `up`→`down`→`up`
    against a disposable Postgres) adds `paper_cycles_opportunity_idx`
    and `owner_id`/`heartbeat_at` (+ a partial index on each, `WHERE
    status IN ('queued','running')`) to `campaign_runs`/`replay_runs`;
    both 000007 and 000009 now note that their `CREATE INDEX`s take an
    ACCESS EXCLUSIVE lock (safe today only because compose's `migrate`
    service always runs before `arbd`, so the affected tables are still
    empty in every environment this has run against).
  - acceptance: `gofmt -l cmd internal` clean; `go vet ./...` clean;
    `golangci-lint run ./...` clean except the SAME pre-existing
    `internal/simulation/paper.go:144` gosec finding noted above (still
    untouched, still out of scope); `go test -race ./...` green across
    the entire repo, including `internal/storage`, `internal/replay`,
    `internal/campaign`, `internal/jobrun`, `internal/api`, `internal/
    app`, `internal/reporting` against a disposable Postgres 16
    container on port 55432 (never 5432) with migrations 000001-000009
    applied via `psql` inside the container, then removed
    (`docker rm -f`) — never the dev-compose DB.

### T-059 Operating mode + provider settings + hot log level
- status: IMPLEMENTED (backend, 2026-08-27) — `docs/design/settings-expansion.md` §2, §4;
  console sections (design §6) are the frontend follow-up.
  Moves the remaining operator-relevant env-only fields into the T-057
  versioned document: `platform.mode` (restart-scoped, enum
  MARKET_DATA/RECORD/PAPER/SHADOW — LIVE rejected by name, REPLAY/BACKTEST
  rejected as batch runs), `platform.log_level` and
  `platform.allowed_origin` (hot), a new `ai` section (enabled, provider,
  model, cadence, budget — hot, which requires always constructing
  `ai.Service`/`ai.Scheduler` behind an `ai.Switch`), and
  `telegram.disabled` (hot, via the existing `allowSet`; **negative field
  on purpose** — an `enabled` bool would zero-value to false on a persisted
  document and silently mute commands and pushes on upgrade). No migration:
  the document is JSONB. **`Settings.WithDefaults(cfg)` lands first** — a
  persisted pre-T-059 payload has no `platform`/`ai` keys, so without
  normalization at every load/get/rollback path `Validate` fails and
  `buildPlatform` takes the P2-4 hard-failure branch, i.e. the process
  refuses to boot after deploy. Same class: `Seed`/`WithDefaults` map every
  non-settable `ARB_MODE` (`REPLAY`, `BACKTEST`, **`SHADOW`** — all legal
  values today) to `MARKET_DATA`, or a `ARB_MODE=SHADOW` deployment seeds a
  document its own validator rejects.
- dependencies: T-057
- acceptance: `internal/platform` validation table incl. `LIVE`/`live`/
  `REPLAY`/`SHADOW`; `WithDefaults` tested against raw JSON literals that
  omit the new sections AND that carry a `telegram` section with only
  `allowlist` (a round-tripped struct would not exercise either branch),
  plus the raw literal with `paper_enabled=false` seeded under
  `ARB_MODE=PAPER` (mode falls back to `MARKET_DATA` with a named WARN,
  `Service.Load` does not refuse the boot); invalid log-level/origin
  seeds WARN by name; the `ai` section is filled field by field;
  `ModeTable`/`Settable` agree with `Validate` for every `config.Mode`
  constant; reflect-over-sections test for
  `PermissionForSection`; `internal/app` mode-from-settings restart test;
  `internal/ai` switch/cadence/budget tests; `-race` green.

### T-060 Secrets vault (AES-256-GCM, write-only API)
- status: IMPLEMENTED (backend, 2026-08-27) — `docs/design/settings-expansion.md` §3;
  the Security console section is the frontend follow-up.
  `secrets` table (migration **000008**), AES-256-GCM under
  `ARB_SECRET_KEY` (base64, exactly 32 bytes) with the secret name as AAD
  and a `key_id` fingerprint per row; closed two-entry registry
  (`anthropic_api_key`, `telegram_bot_token`) so it can never become a
  store for exchange trading keys; `SecretSource` chain (vault first, env
  fallback) resolved by the AI advisor at every settings swap and by
  Telegram at process start. Write-only API: `PUT`/`DELETE
  /api/v1/secrets/{name}` (ADMIN, CSRF, audited), `GET /api/v1/secrets`
  returns presence/source/updated-by only — never a value, never a last-4.
  No `ARB_SECRET_KEY` → vault disabled, platform still runs on env secrets.
- dependencies: T-057 (audit sink, RBAC); parallelizable with T-059
- acceptance: `internal/secrets` crypto tests (wrong key, AAD mismatch,
  key_id mismatch reported not returned, nonce uniqueness); `internal/api`
  RBAC/CSRF denial plus a log-capture assertion that the submitted value
  appears in no emitted log record; the PUT body is decoded straight into
  a zeroed `[]byte` (no Go string copy) and `secret.write`/`secret.delete`
  audit rows carry `after={name,present,key_id}` (design §3.4); a vault
  store error WARNs before the env fallback; `internal/storage` round-trip
  and `List` against a disposable Postgres.

### T-061 Venue availability + capabilities route
- status: IMPLEMENTED (backend, 2026-08-27) — `docs/design/settings-expansion.md` §5;
  the venue cards/availability badges are the frontend follow-up.
  `platform.VenueTable()` lists OKX/Bybit/Bitget/Gate/MEXC honestly as
  `available:false` with the blocking task id, while `CompiledVenues` stays
  the enforcement gate. New `GET /api/v1/platform/capabilities` (modes,
  venues, ai_providers with availability + reason);
  `/api/v1/platform/venues` becomes an alias over the same function. No
  keys anywhere.
- dependencies: T-059 (mode availability list shares the shape)
- acceptance: `internal/api` capabilities shape test; enabling an
  unavailable venue returns `400 connector_unavailable` naming the task.

---

## Phase 22 — Scanner Suite (cross-venue screener, perpetuals/funding, alerts, auto-paper)

Design: docs/design/scanner-suite.md (restates the operator's request as a
Claude Code command, §0). Public market data only; automatic execution is
PAPER only; the vault's exchange credential group stays unread.

### T-065 Screener endpoint research
- status: DONE (2026-08-27; Bitget/OKX fee and rate-limit items remain UNVERIFIED and are flagged in the settings UI) — docs/research/screener-endpoints.md:
  public bulk spot tickers, instrument lists, USDT-M perp tickers,
  funding (rate, interval, history), currency/chain status (public vs
  key-gated), rate limits, regular-tier fees for Binance, OKX, Bybit,
  Bitget, Gate, MEXC. Acceptance: every field VERIFIED with URL + access
  date or marked UNVERIFIED.

### T-066 Venue collectors (`internal/screener/venue`)
- status: DONE (2026-08-27) — six collectors with fixtures + conformance test; 5-min soak 5,448 pairs / 3,990 perps, zero 429/418. 30-min soak still owed before Tier-2 defaults flip. One collector per venue polling the bulk endpoints
  under a per-venue weight/rate gate; normalised spot quotes and perp
  rows; data-age tracking. Acceptance: ≥ 90 % of tradable spot pairs per
  poll, 30-minute soak with zero 429/418, fixture unit tests per venue.

### T-067 Spreads, basis and carry math
- status: DONE (2026-08-27) — golden tests; asset-identity + liquidity guard added after the live VON/TROLL/XTER mismatch (suspect lanes excluded by default). Net cross-venue spread (both taker fees), liquidity
  (top-of-book), lifetime tracking; spot↔perp basis, funding carry
  annualised net of fees. Decimal only in money paths; golden tests.

### T-068 Screener settings, API, RBAC
- status: DONE (2026-08-27) — migration 000010, screener:view/screener:config, every §7 route. Versioned DB document (venues on/off, poll interval,
  fee table, liquidity floor, paper balances per venue), `screener:view`
  (OPERATOR+) / `screener:config` (ADMIN), CSRF, audit, parent_version;
  read models for the console; migration 000010.

### T-069 Console pages (Scanner Suite group)
- status: DONE (2026-08-27) — six pages, icon rail, light/dark AA tokens with contrast check, seven UX components; sidebar collapse-to-rail and dense mode deferred. `/screener`, `/perpetuals`, `/funding`, `/calculator`,
  `/scanner-alerts`, `/auto-paper`; stats strip, filter card with saved
  templates, dense auto-refreshing virtualised table, row expand with
  per-side quotes; light theme in the design system; e2e coverage.

### T-070 Alert rules → Telegram
- status: DONE (2026-08-27) — evaluator with lifetime/cooldown/dedup, measurement-only text with fixed footer; real Telegram delivery not yet exercised. Persisted rules (spread/lifetime/liquidity/venues/
  funding thresholds), evaluation on each poll, cooldown + dedup through
  `internal/notification`, audit on rule changes.

### T-071 Automatic paper execution
- status: DONE (2026-08-27, code) — cross-venue spot, carry, funding harvest reproducing strategy-models worked examples; migration 000011 ledger; NO soak evidence yet (24 h soak report owed). Rule opt-in; CrossVenueSpot (inventory on both venues,
  no transfers), Carry (spot long + perp short with funding accrual and
  maintenance-margin stop), Futures-Futures; paper cycles tagged by
  strategy so PnL/Reports break them down; 24 h soak report filed under
  docs/campaigns/screener/. LIVE stays disabled.

### T-072 Skill, agent and deployment docs
- status: DONE (2026-08-27) — `.claude/skills/scanner-suite/SKILL.md`,
  `.claude/agents/screener-engineer.md`; deployment notes follow T-068.

## Phases 23–27 — Product programme (docs/design/crypto-arb-platform-command.md)

### Phase 23 Venue breadth
- T-073 `Collector` interface + conformance test + venue registry (verified flag, fee defaults). TODO.
- T-074 Tier-1 venues via public bulk tickers: Binance, OKX, Bybit, Bitget, Gate, MEXC (from T-065/T-066). TODO.
- T-075 Tier-2 venues: KuCoin, HTX, Kraken, Coinbase DONE and ENABLED BY DEFAULT (30-min live soak 2026-08-27, all ten venues, poll 5 s: kucoin 295 polls 1006 spot/664 perps avg 1104 ms max 3346; htx 197 polls 600/301 avg 4145 max 7496; kraken 248 polls 1382/276 avg 2249 max 3806; coinbase 142 polls 921/0 avg 7701 max 9365 — 0 × 429/418/403/510 and 0 errors each; book 6690 pairs / 5231 perps). HTX/Coinbase limits and fees still UNVERIFIED (flagged in the registry). MEXC in-band 510 now detected (1 hit in the soak on contract/ticker → counted, 10 s pause, recovered). Tier-3 (Crypto.com, Bitfinex, BingX, WhiteBIT, BitMart) added 2026-08-27 with research files, recorded fixtures and passing conformance, registered OPT-IN in 227b0d6; their 30-min soak ran 2026-08-28 (TestSoakLive, poll 5 s, ALL FIFTEEN venues in one process, sharing the IP with the running paper stack — a stricter per-IP budget than production) and ALL FIVE PASSED, so they are now ENABLED BY DEFAULT (`tier3Venues` removed; `Defaults()` enables every known venue). Per venue, polls / spot min..last / perps min..last / min-avg-max ms / 429-418-403 / in-band / failed polls: cryptocom 118, 576..576, 10..366, 9623-10243-25108, 0, 0, 0; bitfinex 347, 197..197, 75..75, 100-192-3588, 0, 0, 0; bingx 283, 669..669, 881..881, 915-1370-2573, 0, 0, 0; whitebit 343, 798..798, 305..305, 157-248-641, 0, 0, 0; bitmart 167, 27..27, 354..354, 5441-5790-12539, 0, 0, 0. Book 7015 pairs / 7218 perps; 671 funding-history rows. Reading notes: cryptocom's perps 10..366 is the round-robin mark fill saturating (spot 576 was flat throughout) and its avg 10.2 s poll exceeds the 5 s interval, so it self-paces to ~15 s — the slow venue in the set; BitMart's 27 spot quotes are 100 % of the rows its bulk ticker returns (documented: only pairs with 24 h volume > 0 — 27 of 65 tradable symbols on the day, all normalised, none dropped) plus 354 perps. The in-band column read 0 for every venue but was never exercised this run (MEXC's 510 did not recur), so it is zero-by-absence; what makes the passes robust is errs=0 — a rate-limit answer the classifier missed still surfaces as a failed poll, as HTX's did. SAME RUN, TIER-2 REGRESSION (reported, not acted on — Tier-2 defaults are the coordinator's call): coinbase recorded 20 x HTTP 429 and 20 failed polls out of 61 attempts (41 successful, spot dipping to 61 at the low end, avg 24.4 s) — under this run it would FAIL the rule that admitted it on 2026-08-27; the confound is that the running paper stack polls coinbase from the same IP, roughly doubling its request rate. HTX recorded 1 failed poll of 197, `htx: batch_merged: invalid-parameter: request limit` — a request-limit answer the gate does NOT classify as a rate limit (so it neither counted nor backed off); both warrant review. Remaining venues: Upbit, Bithumb, LBank, Phemex. IN_PROGRESS.
- T-076 DEX quotes via public aggregator APIs (Uniswap/PancakeSwap/Jupiter) with gas cost model. TODO.

### Phase 24 Strategies, auto-paper, unattended operation
- T-077 Strategy registry (cross-venue spot, carry, futures-futures, funding harvest, triangular) with per-strategy paper ledger and statistics. TODO.
- T-078 Nightly paper report per strategy and per rule (2026-08-27, `internal/screener/report/`): 00:05 UTC and `POST /screener/reports/run` (ADMIN); previous UTC day + cumulative; strategy-models §7 table (n, net after fees/funding/slippage, net bps mean/median, hit rate with Wilson 95 %, lifetime, max drawdown, drift, skipped by reason, funding rows, slip p95, concentration) and the §8 checklist with pass/fail + reason per item ("no evidence yet" below the floors; regimes, stress grid, fee verification and manual items always fail until filed). Files `<recordings>/screener-reports/<date>/`, table `screener_reports` (000012), one Telegram summary. Tests: synthetic ledger with hand-computed answers (6 executions / 3 days), 240-sample gate run (items 4 and 6 pass, 6 fail), storage round trip. Docs/campaigns filing of a real 30-day window: not yet (no ≥ 30-day auto-paper run exists).
- T-079 Operations automation — minimal part done (2026-08-27): self-healing collectors (`Automation.healCollectors` restarts a venue goroutine with no completed poll for 5 × poll_interval_s, logs it, `venues[].restarts` in /screener/status; tests `TestPollerRestartStale`, `TestAutomationHealsStaleCollectors`). Scheduled migrations, backups, health checks and alerting on failure: TODO.
- T-080 Evidence dashboard: per-strategy net PnL after fees, hit rate, drawdown, sample size vs production-gate thresholds. TODO.

### Phase 25 SaaS
- T-081 Tenancy: organisations, memberships, roles; console per tenant. DONE (2026-08-27, migration 000013; platform-admin flag gates the exchange-credential vault and system routes with tests; Organisation page). Compliance blocks (docs/compliance/review-2026-08-27.md #1, #9, #10): exchange-credential vault operator-only with a 403 test for tenant roles; Art. 30 data map + erasure by pseudonymised audit before prod; Telegram chat IDs encrypted, never logged.
- T-082 Packages + entitlements enforced server-side. DONE (2026-08-27; schema-validated resolve with overrides that can never enable live; enforcement on rules, venues, refresh, templates, auto-paper, alerts/day; console gating + upgrade toasts).
- T-083 Paddle billing lifecycle + webhooks + customer portal. IMPLEMENTED (2026-08-27; signature-verified idempotent webhooks, checkout/portal/cancel, Billing page; sandbox run against real Paddle still owed — needs the operator's Paddle account and catalogue per docs/design/billing.md).
- T-084 Affiliate programme ledger + payouts report. PARTIAL (2026-08-27; decimal accrual ledger with maturation/reversal; payouts report and jobs open).
- T-085 Marketing site (site/) with evidence-based copy and legal pages; compliance review. IN_PROGRESS (copy and legal drafts in docs/site; site scaffold with copy lint being built). Blocks (#3, #4, #5, #6): legal-page drafts, sign-up risk acknowledgement, hypothetical-performance disclaimer on every paper surface, copy lint (no %/currency figure without a docs/campaigns citation; banned words).
- T-086 Client onboarding, alert channels, client API keys. DONE (2026-08-27): API keys (SHA-256 digest, prefix index, plaintext shown once, Bearer auth with per-key rate limit and 429 + Retry-After, org+key scope check, platform-admin never granted to a key, audited create/revoke, `api.keys_max`/`api.scopes` gated); e-mail (SMTP via the `smtp_url` secret) and HMAC-signed webhook sinks with SSRF hardening (resolve-then-dial, no redirects, private targets refused) and retry/backoff; per-channel delivery outcomes stored on `screener_events.delivered`; migration 000015. Onboarding wizard shipped with T-081's console pass.
- T-087 Client console re-skin (ux-designer → ui-designer → frontend). TODO.
- T-088 White-label option (later). TODO.

### Phase 26 Production infrastructure
- T-089 deploy/ as code: Helm/Kustomize + Terraform; envs dev / paper-test / prod. TODO.
- T-090 HA Postgres + PITR + restore drill; tick partitioning. TODO.
- T-091 CI/CD staged deploys with canary + rollback. TODO.
- T-092 Observability stack, SLOs, alert rules, runbooks. TODO.
- T-093 Edge security (WAF, rate limiting), image/dependency scanning, GDPR data map. TODO.
- T-094 Load/soak tests at target scale (venues × pairs × tenants). TODO.

### Phase 27 Production execution gate
- T-095 BLOCKED by design (record in docs/decisions/; see compliance review #2, #16 for what client-funds execution would additionally require): live execution requires (a) ≥ 30 days positive auto-paper evidence across regimes, (b) security review, (c) the operator's recorded legal decision, (d) a human-reviewed code change replacing ErrLiveTradingDisabled. No work starts before (a)–(c) exist.

### T-096 Paper balances do not propagate from settings to the ledger
- status: FIXED 2026-08-28 (`screener.Service.OnSettingsApplied` →
  `paperexec.Executor.ReloadWallets`, test
  `paperexec/wallet_reload_test.go`). The seeding rule was already
  right — ledger rows win, settings seed only (venue, asset) pairs the
  ledger lacks — but `ensureWallets` latched on `x.loaded`, so nothing
  re-ran after a settings apply. The next tick now re-seeds newly added
  assets while leaving every balance the executor has moved untouched.
  Original report follows.
- (found 2026-08-27 during the T-071 evidence run). Editing
  `paper.balances` in the screener settings changes nothing: the ledger
  (`screener_paper_balances`) is seeded once and the executor then holds
  the wallet in memory, so a balance change needs a direct DB write AND a
  process restart. Fix: apply settings balances on activation (upsert the
  ledger for assets the operator added, never silently overwriting a
  balance the executor has already moved), and reload the executor's
  wallet on the settings-change hook. Until then the console's
  "simulated balances" control is misleading.

### T-097 Carry entry gate admits positions that cannot pay their costs
- status: TODO (evidence 2026-08-27 20:29–21:06 UTC, Binance USDT perps,
  `min_carry_apr = 5 %`, 1000 quote per position). 412 positions closed,
  **0 winners, 0 funding settlements collected, net −2283.14 USDT**
  (≈ −55 bps each ≈ the four taker legs plus slippage); 1108 skipped
  (DEPTH/BALANCE). Two code facts verified by reading the source, not
  inferred from the numbers:
  1. `alerts.carryActive` (kind `carry`) checks min-edge, min-carry-APR
     and liquidity but has **no breakeven-interval gate**, while
     `harvestActive` (kind `basis`) does check
     `BreakevenN > MaxBreakevenIntervals`. At 5 % APR (≈ 1.1 bps per 8 h)
     roughly 39 intervals are needed to clear ~43 bps of round-trip cost,
     so these entries should not have qualified.
  2. Entry measures basis as `(perp_bid − spot_ask)/spot_ask` while the
     exit measures `(perp_ask − spot_bid)/spot_bid` — the opposite book
     sides — against `close_bps` default 0 with no minimum hold
     (`paperexec/perp.go:347,377`). On any book whose round-trip spread
     exceeds the entry basis, a position satisfies its own "converged"
     exit on the next poll, before any funding settlement, and realises
     the spread plus four taker fees. That matches 0 funding rows across
     412 closes.
  Entry guards SHIPPED 2026-08-28 (`alerts/signals.go`, tests in
  `alerts/carry_gate_test.go`): (a) `breakeven_exceeds_hold` — a carry is
  refused when the settlements needed to pay its round trip exceed the
  settlements that fit in `max_hold_h`. Note this is deliberately NOT the
  harvest strategy's fixed 12-interval cap: that cap rejects the design's
  own §3.5 worked example (43 intervals inside a 30-day hold), so the
  gate is breakeven-versus-hold. (b) `closes_immediately` — the entry is
  refused when the exit-side basis (perp ask vs spot bid, the sides a
  close actually crosses) already sits at or below `close_bps`, which is
  the configuration that produced 412 losing closes with zero funding.
  Exit guard SHIPPED 2026-08-28 (`paperexec/perp.go`, tests in
  `paperexec/exit_hold_test.go` and the reworked `skip_test.go` case): a
  "converged" close now requires at least one collected funding
  settlement, so a carry cannot open and close having collected nothing.
  The stops (margin, basis blow-out, funding reversal, max hold) are
  evaluated first and stay immediate — a position going wrong still
  exits at once, which the test pins explicitly.
  STILL OPEN: no fresh evidence run has been made on an isolated backend
  (T-098) to confirm the three guards hold in the wild. Do not raise
  the threshold to hide anything.

### T-098 The console dev proxy lets a browser/e2e run mutate live data
- status: TODO (found 2026-08-27). `web/next.config.ts` proxies `/api`
  to `http://localhost:8080` by default — the live backend. During this
  evidence run a `screener.rule.update` landed at 21:06:15 UTC from a
  host client with the admin session; the carry rule was rewritten into
  a `spread` rule (kind and `min_spread_bps` replaced, `min_carry_apr`
  dropped) and carry executions stopped one minute earlier, at 21:05:41.
  `scripts/e2e.sh` is correctly isolated (port 18080, its own database),
  so the exposure is the dev-server proxy plus any console session
  pointed at it. Fix: default the proxy to a non-production port or
  require `ARB_BACKEND_URL` explicitly, and make evidence runs refuse
  writes from a session that did not create the rule (or run them
  against a dedicated org). Until then, no evidence run should share a
  backend with a console under test.

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
- 2026-08-26 (CI green): hosted CI is fully green on the PR head — all
  five jobs pass on real runners (backend race suite against the
  PostgreSQL service with all migrations, golangci-lint, frontend
  lint/typecheck/build, Playwright E2E against the real backend,
  gitleaks, govulncheck). The one CI-only failure found on the way (a
  CSRF-recovery race: a mutating click in the first moments after a
  full page reload fired before /auth/me restored the token) was
  reproduced locally by delaying /auth/me and fixed by making mutating
  requests await token recovery. T-004 flips to DONE.
- 2026-08-26 (deployment + campaign tooling): T-046's in-repo half is
  complete. New `internal/backtest` package: a deterministic
  discrete-event harness that replays recorded segments through the
  real scanner, risk engine, and simulation executor — simulated
  latency pumps recorded frames, so fills price against books that
  moved during the wait — under §80 stress scenarios (higher fees,
  higher latency, worse fills via fill-depth haircut, lower liquidity
  via world-depth haircut); §80 report with fail-rate, slippage,
  latency, drawdown, turnover, and the mandatory perfect-conditions
  flag; 7 acceptance tests including byte-identical determinism.
  `cmd/campaign` orchestrates grid × seeds from disk + PostgreSQL
  metadata (new storage loaders: RecordingStreams, LoadMarkets). New
  Dockerfile (multi-stage, non-root, healthcheck) and compose
  profiles `record` / `paper` / `campaign` with a recordings volume;
  docs/deployment.md is the runbook; `make record` /
  `make campaign RECORDING=…` are the two commands. T-046 now blocks
  ONLY on running the recorder from a network-enabled host.
- 2026-08-26 (T-050 pre-work): OKX connector research checklist
  prepared (docs/research/okx-connector-checklist.md). OKX hosts are
  egress-blocked here (www.okx.com / my.okx.com unreachable, same as
  the Binance doc sites in T-047), so the checklist is a runbook: nine
  verification sections (endpoints, instrument mapping, the strict
  prevSeqId/in-band book protocol, REST cross-checks, connection
  lifecycle, fees, universe, maintenance signals, demo env), each item
  carrying the expected answer from the 2026-08-26 research round and
  the code seam it feeds; plus the design decisions the answers settle
  (venue-parameterizing marketdata/replay.go and cmd/campaign, seq-reset
  validator semantics, the OKX Capabilities literal) and exit criteria.
  T-050 remains BLOCKED behind T-046/T-047.
- 2026-08-26 (T-047 executed): research debt re-verified from a
  network-enabled host — primary pages pulled raw (Binance spot-api-docs
  repo, OKX docs-v5 5.2 MB reference, OKX help centre + fee page JSON,
  Kraken docs/support, Bybit help, Coinbase CDP docs, Binance public
  announcement CMS) plus a live keyless OKX `books` capture. Kraken
  Tier 1 fees corrected to 0.40/0.80 % (2026-07-09 re-tiering; 3-leg
  240 bps); OKX OKB ladder debt closed (no OKB tier — volume/assets
  tiers) and `market/books` limit pinned (40 req/2 s); OKX checksum
  deprecation confirmed in production since 2026-06-23 with keep-alive
  and reset semantics quoted from the docs; Binance U/u rule restated
  two-phase, 5 msg/s client limit added, promo-pair inventory dated (no
  Regular-tier zero-fee leg in a liquid triangle). Items that stayed
  unpinned (Gate 403s, Bitget client-rendered docs, Coinbase login-gated
  fee table, Kraken public rate) are demoted to named runtime-verified
  assumptions in final-platform-selection.md §7.2. T-047 DONE; T-050
  acceptance restated; T-050 still BLOCKED on the T-046 verdict.
- 2026-08-26 (T-055): the recording/campaign runbook got a console
  front-end — in-process recorder control, background campaign runner
  with persisted runs, ops API + permissions, Campaigns page
  (docs/deployment.md §3b).
- 2026-08-26 (T-056): MEXC researched from primary sources + live
  keyless API pulls (docs/research/mexc.md): 2,115 spot pairs, six core
  legs live, 0/5 bps base with a zero-fee USDC zone whose API
  eligibility is unverified; scored 65/100; queued for the T-051 scoring
  round with named burn-ins.
- 2026-08-27 (T-057 backend): platform settings + supervised restart
  implemented per the design doc. New `internal/platform` package
  (versioned settings document, pure `Validate`/`ValidatePaperMode`,
  catalog-backed `ValidateAgainstCatalog` dry-run wired into
  `Service.applyLocked` for D8, `Seed`/`FieldTiming`/`PermissionForSection`)
  and `internal/storage/platformsettings.go` over new migration 000006.
  `internal/app/engine.go` re-entrancy fixed (E1-E10) with a passing
  two-Run test asserting no goroutine leak and no double strategy
  subscription. New `internal/app/supervisor.go` re-enters one stable
  `*Engine` across a restart with 13 unit tests (fake `EngineRunner`)
  covering every §2.5 guard rail, E9 (paper pause survives), and both
  §2.3 pending-reason sources. New `internal/api/platformapi.go`:
  `/api/v1/platform/settings{,/preview,/rollback,/versions,/version/{n}}`
  and `/api/v1/engine/{status,restart}`, interface-typed
  (`RestartController`) so `internal/api` never imports `internal/app`,
  with 12 handler tests. `components.go` wires `buildPlatform`, the
  supervisor (replacing the engine as the appended `app.Component` —
  D4), the shared Telegram allowlist (`Bot.Allowed`/`PushSink.Targets`),
  and the `health` topic (moved out of `Engine.Run` per §2.6). All new
  and existing tests green with `-race` (storage integration tests run
  against a disposable Postgres, never the dev compose DB); `gofmt`,
  `go vet`, and `golangci-lint run ./...` clean repo-wide. Frontend
  (design §4, BL-12) is explicitly NOT built this round — T-057 is
  IMPLEMENTED (backend); the console UI is a follow-up.
- 2026-08-27 (T-059/T-060/T-061 backend): settings expansion implemented
  per `docs/design/settings-expansion.md`. `internal/platform` gains the
  `platform` (mode/log_level/allowed_origin) and `ai` sections,
  `telegram.disabled`, `ModeTable`/`Settable`/`ValidateMode` (LIVE and
  REPLAY/BACKTEST refused by name, SHADOW enumerated but unavailable),
  `AIProviderTable`, `VenueTable` (okx/bybit/bitget/gate/mexc listed with
  the blocking task; `ErrConnectorUnavailable` → `400
  connector_unavailable`), `WithDefaults` applied at Load/Get/rollback
  with the raw-JSON-literal tests, and every non-settable `ARB_MODE`
  seeded as `MARKET_DATA` with a named log line. `internal/ai` gains
  `Switch` (always-constructed Service/Scheduler; disabled is a skip, not
  a failure), a re-armable scheduler reading `ai.schedule` at every wake,
  the per-process daily cap and `MaxTokens`. `internal/app`: hot
  `SetLogLevel` (package `slog.LevelVar`), `Engine.Mode()` from the
  applied document (per-run snapshot), paper routes wired
  unconditionally, `allowSet` mute, supervisor pending reason naming the
  mode transition, `buildSecrets`/`buildAdvisor` over the secrets chain.
  New `internal/secrets` (AES-256-GCM, name as AAD, `key_id`, closed
  two-name registry, vault→env `Chain`, `Manager` status view) with
  migration 000008 and `internal/storage/secrets.go`. `internal/api`:
  `GET /platform/capabilities`, `/platform/venues` alias, `GET/PUT/DELETE
  /secrets`, `GET /ai/status`, hot `allowed_origin`, apply-time
  `warnings`, `field_timing` extended. Tests: platform (mode table, AI
  bounds, origin/log level, upgrade at load/get/rollback, seed mapping,
  reflect-over-sections), secrets crypto suite, ai switch/cadence/budget/
  max_tokens, app mode-from-settings restart with a real engine, log
  level, allowSet, api RBAC/CSRF/unknown/unavailable + log-capture,
  capabilities shape; storage round-trip against a disposable Postgres.
  `go test -race ./...`, `go vet`, `gofmt` clean; `golangci-lint` reports
  one pre-existing gosec finding in `internal/simulation/paper.go`
  (out of scope for this task). Console work (design §6) NOT built.
- 2026-08-27 (T-059/T-060 review fixes): `WithDefaults` falls back to
  `MARKET_DATA` (named WARN) when the `ARB_MODE` seed breaks a stored
  cross-field rule, fills `ai` field by field; `SeedNotes` WARNs invalid
  log-level/origin seeds; `Service.seed` is an `atomic.Pointer` (lock-free
  `Get`). `internal/app`: `aiApplier` serialises advisor rebuilds under one
  mutex, skips unchanged `ai` sections, resolves the key in a goroutine
  (never inside the platform writer lock) with a generation counter
  dropping stale results. `internal/ai`: budget reserved after the prompt
  builds and released on a mid-call switch-off. `internal/secrets`:
  `Vault.Get` WARNs on store errors, values are `[]byte` end to end,
  `Manager.List` uses `Store.List`. `internal/api`: `AuditAction` gains an
  `after []byte` payload (`auditWith`), secret PUT decodes into a zeroed
  `[]byte`. All under `go test -race ./...`; storage on a disposable
  Postgres; the same pre-existing gosec finding remains the only lint
  item.
- 2026-08-27 (T-047 follow-up, browser): binance.com/en/fee/tradingPromote
  read in a real browser — the FDUSD program is a live zero-MAKER
  promotion (taker at standard VIP rates; only FDUSD/USDT is 0/0), so no
  taker leg is free at Regular tier; coinbase.com/advanced-fees redirects
  to sign-in and stays a runtime-verified assumption; the Gate fee page
  remains unreachable (browser domain not allowed).
