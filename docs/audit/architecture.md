# Platform audit — architecture and trading-flow map

Audited tree: `master` at `abddb55` (2026-09-09). This document records what
the repository actually contains and how a triangular-arbitrage opportunity
travels from an exchange frame to a settled paper cycle. It describes the
implementation as found; the judgement of each stage lives in the companion
audit documents in this directory.

Safety posture confirmed before any inspection: no `.env` file is present;
the only execution boundary that can place real orders is
`execution.LiveExecutor`, which returns `ErrLiveTradingDisabled` on every
call; `platform.mode` rejects `LIVE` by name; no exchange trading key can be
stored (the secrets registry is closed to two names). Every conclusion below
is about PAPER evidence and about readiness for a future live mode.

## 1. Stack and process topology

| Layer | Implementation |
|---|---|
| Backend | Go 1.25 monolith `arbd` (`cmd/arbd`), `net/http` API, `gorilla/websocket`, `pgx/v5`, `shopspring/decimal` for every money path, OpenTelemetry metrics with a Prometheus exporter, `slog` logging |
| Persistence | PostgreSQL 16, 15 forward-only migrations (`migrations/000001..000015`), in-process outbox for hot-path decoupling |
| Console | Next.js App Router + TypeScript + Tailwind (`web/`, 36 pages, ~20 k lines) with Playwright E2E (`web/e2e`) |
| Marketing site | `site/` (separate Next.js app, own CI workflow) |
| Control surfaces | Web console, Telegram long-polling bot (`internal/telegram`), REST API keys for tenants (`internal/apikey`) |
| Delivery | Dockerfile + `docker-compose.yml` (dev/record/campaign profiles), Helm chart with dev/paper-test/prod values, Terraform module skeletons, Prometheus rules and Grafana dashboards under `deploy/` |
| CI | `.github/workflows/ci.yml` (Go build/vet/test with Postgres service, `-race`, golangci-lint, gitleaks, govulncheck, frontend lint/typecheck/build, Playwright E2E), `site.yml`, `deploy.yml` |

One process hosts every component; `app.BuildComponents` selects a subset by
profile (`cmd/api`, `cmd/scanner`, `cmd/recorder`, `cmd/replay`, `cmd/worker`,
`cmd/campaign`). Components talk through typed in-process channels; there is
no message bus. Operating modes are `MARKET_DATA`, `RECORD`, `PAPER`,
`SHADOW` (console-selectable, restart-scoped through `app.Supervisor`) plus
the batch entry points `REPLAY` and `BACKTEST`.

## 2. Package inventory (non-test lines / test lines)

| Package | Responsibility | Code | Tests |
|---|---|---|---|
| `internal/exchange` (+`/binance`) | normalized market models, instrument rules, Binance REST/WS transport, decoder, sequence validator and syncer, request-weight gate | 279 + 1070 | 160 + 713 |
| `internal/marketdata` | feed orchestration, raw-frame recorder and recorder control | 753 | 292 |
| `internal/orderbook` | `Book` (single writer, RWMutex), health state machine, `Set` registry with coalescing dirty tracker | 487 | 293 |
| `internal/graph` | conversion graph and canonical directed-triangle enumeration | 175 | 208 |
| `internal/pricing` | depth walks (VWAP), fee placement, quantization, cycle chaining, size search | 415 | 419 |
| `internal/fees` | fee schedules, per-market overrides, venue fee conventions, token-discount table | 193 | 137 |
| `internal/opportunity` | opportunity record, buffers, TTL, status table, revalidation contract | 171 | 155 |
| `internal/risk` | limit evaluation with scoped overrides, reason codes, breaker registry | 469 | 278 |
| `internal/scanner` | dirty-market worker pool: re-price affected triangles, size, buffer, risk-gate, emit events | 290 | 317 |
| `internal/execution` | `Executor` interface, plan/result types, outcomes, disabled `LiveExecutor` | 141 | 0 |
| `internal/simulation` | paper executor: seeded latency, limit-IOC filtering, fill-time book reads, exposure, settlement | 446 | 417 |
| `internal/paper` | paper loop: reserve → execute → settle → portfolio, pause/resume/reset, counters | 245 | 356 |
| `internal/reservation` | atomic capital ledger: reserve/settle/release/credit, conservation invariants | 285 | 258 |
| `internal/portfolio` | realized PnL, exposure, fees, equity, drawdown, book-based marker | 283 | 252 |
| `internal/strategy` | versioned strategy parameters (scanner config + risk limits), validation, diffs | 775 | 208 |
| `internal/platform` | versioned platform settings (symbols, balances, venues, fees, mode, AI, Telegram) | 1491 | 1166 |
| `internal/app` | wiring, engine run loop, supervisor (restart), read models, metrics registration | 4915 | 2395 |
| `internal/api` | REST + WebSocket server, middleware, all route handlers | 4858 | 4839 |
| `internal/auth`, `internal/apikey`, `internal/tenancy`, `internal/entitlements`, `internal/billing/*` | sessions/RBAC/CSRF, tenant API keys, organisations and memberships, package documents and gates, Paddle + affiliates | 932 / 272 / 349 / 1089 / 1276 | 597 / 0 / 0 / 691 / 518 |
| `internal/storage` | repositories, outbox, analytics, retention | 4757 | 3044 |
| `internal/realtime`, `internal/notification`, `internal/telegram`, `internal/ai`, `internal/reporting`, `internal/metrics`, `internal/secrets` | WS hub, alert routing, bot, advisor with human approval, reports, OTel registry, encrypted vault | — | — |
| `internal/screener` (+`alerts`, `paperexec`, `report`, `venue`) | Scanner Suite: 15 public-data collectors, cross-venue/perp signals, automatic paper execution, nightly reports | 2907 + 1200 + 1875 + 1545 + 5380 | 1262 + 893 + 958 + 591 + 1710 |
| `internal/replay`, `internal/backtest`, `internal/campaign`, `internal/quality`, `internal/jobrun` | recorded-session replay, backtest driver, campaign runner, execution-quality score, job ownership | — | — |

Packages with no tests at all: `internal/execution`, `internal/apikey`,
`internal/tenancy`, every `cmd/*` main.

## 3. Triangular trading flow — stage by stage

```
Binance WS depth frames / REST snapshots
  → binance.Feed.session / handleFrame (transport, decode, recv timestamp)
  → binance.Syncer (snapshot splice, U/u/pu sequence rule)
  → orderbook.Book.Apply (single writer; HEALTHY/STALE/CORRUPTED/SYNCING/DISCONNECTED)
  → orderbook.Set.MarkDirty (coalesced) → scanner.Run worker pool
  → scanner.EvaluateTriangle: views ×3 → HEALTHY pre-gate → pricing.CapacityHint
      → pricing.SizeSearch over pricing.QuoteCycle (VWAP ×3, fees, quantization)
      → opportunity.Build (latency + risk buffers, TTL) → risk.Evaluate
  → scanner.Out (bounded, drop-counted)
  → app.Engine.consumeEvents: outbox (qualified opportunity, throttled risk rejects),
      realtime hub, notifications, paper queue (PAPER mode only)
  → paper.Engine.runCycle: TTL check → reservation.Reserve (idempotency key =
      opportunity id, per market-side conflict keys) → simulation.Engine.ExecuteCycle
      (per leg: seeded submit latency → fill-time book read → limit-IOC filter →
       pricing.QuoteLeg → seeded fill latency) → settle → reservation.Settle/Credit
      → portfolio.ApplyCycle → OnResult (log, metrics, notification, outbox cycle
      record, hub "cycles" topic)
  → storage.Outbox.Run (single writer, bounded queue) → PostgreSQL
      (opportunities, paper_cycles, orders, fills, risk_events, …)
  → internal/api read models + realtime topics → web console / Telegram / reports
```

| Stage | Location | Inputs → outputs | Concurrency | Failure handling | Tests |
|---|---|---|---|---|---|
| Transport + decode | `exchange/binance/feed.go`, `transport.go`, `decoder.go` | WS frame → `orderbook.DepthEvent` with receive timestamp; raw tap to recorder | one reader goroutine per session; books written only here | read error → every book `MarkDisconnected`, reconnect loop in `Feed.Run` | `binance_test.go`, `chaos_test.go`, `race_test.go` |
| Sequence + splice | `exchange/binance/validator.go` | buffered events + REST snapshot → Apply/Drop/Gap/Reset; gap → `MarkSyncing` + resync | same goroutine | resync per market with snapshot fetch | `binance_test.go` |
| Book state | `orderbook/book.go`, `registry.go` | deltas → ladders, version, `State`; `EvaluateStaleness` sweep every 500 ms in `app.Engine.Run` | RWMutex per book; `View` copies top-K | STALE when no update within `max_book_age_ms`; `Set.View` returns a view in ANY state | `book_test.go` |
| Triangle topology | `graph/graph.go`, built in `app.Engine.Run` | tradeable markets × starting assets → directed triangles + market index | built once per run (restart-scoped) | untradeable/bad-rule markets counted in `Rejected` | `graph_test.go` |
| Pricing | `pricing/pricing.go`, `sizer.go` | views + rules + fee schedule + input → `CycleQuote` | pure functions on the evaluator goroutine | dust / min-notional / no-depth errors reject the size | `pricing_test.go` |
| Opportunity + risk gate | `opportunity/opportunity.go`, `risk/engine.go`, `scanner/scanner.go` | sized quote → buffered net edge → `Decision` with every check recorded | evaluator pool (`workers`) | rejection reason codes; events dropped with a counter when `Out` is full | `scanner_test.go`, `risk_test.go`, `opportunity_test.go` |
| Fan-out | `app/engine.go` `consumeEvents` | events → outbox records, hub payloads, paper queue | one goroutine; non-blocking sends | paper queue full → warning log only | `engine_test.go` |
| Paper loop | `paper/engine.go` | qualified event → reservation → plan → executor → ledger/portfolio → `OnResult` | semaphore `MaxConcurrent` (default 3) | executor error → release; REJECTED/EXPIRED → release; otherwise settle + credit | `engine_test.go` |
| Simulation | `simulation/paper.go` | plan → per-leg fills against fill-time views → `CycleResult` (outcome, PnL, exposure, fees, slippage) | one goroutine per cycle; RNG derived per (seed, cycle, leg) | leg failure → exposure in held asset; ctx cancel → TIMEOUT | `paper_test.go` |
| Ledger | `reservation/reservation.go`, `portfolio/portfolio.go` | reserve/settle/credit; realized, exposure, fees, equity, drawdown | one mutex each | `CheckInvariants` exists; see execution audit for whether it runs | `reservation_test.go`, `portfolio_test.go` |
| Persistence | `storage/outbox.go`, `storage/*.go` | records → INSERTs | single writer goroutine, bounded channel | overflow/error policy documented in the database audit | storage tests (DB-backed, skipped without a DSN) |
| Read side | `api/*.go`, `app/readmodel.go`, `realtime` | tables + in-memory status → JSON envelopes, WS topics | per-request | — | `api/*_test.go` |

## 4. Scanner Suite flow (public data, automatic paper)

```
15 venue collectors (screener/venue/*.go, REST polling at poll_interval, rate gate with
 429/418/403/510 classification) → screener.Book (spot quotes, perps, funding)
 → alerts.ComputeSignals per rule (spread / carry / basis / funding / triangular lanes,
   data-age and liquidity guards, entitlement checks)
 → alerts.Dispatch (cooldown, dedup, channels) → screener_events, Telegram, hub
 → paperexec.Executor.OnOpen / Tick (per-venue reservation wallets, top-of-book fill with
   slippage allowance and seeded latency, funding accrual, exit rules) → ledger tables
 → report.Generator (nightly statistics + production-gate checklist) → screener_reports
```

The screener has its own paper path, separate from `internal/simulation`; the
two share `internal/reservation` and the seeded-latency idea but not the
fill model (the screener fills top-of-book with an allowance, the triangular
engine walks depth).

## 5. Control surfaces (mutating routes)

`POST /api/v1/paper/{pause,resume,reset}`, `POST /api/v1/engine/restart`,
`POST /api/v1/config` (+`/rollback`), `POST /api/v1/platform/settings`
(+`/preview`, `/rollback`), `POST /api/v1/recordings/{start,stop}`,
`POST /api/v1/replays`, `POST /api/v1/campaigns`, screener rules/templates/
settings, AI recommendation approve/reject, alerts ack/resolve, users,
organisations, members, API keys, billing checkout/cancel/webhook, secrets
write/delete. Telegram mirrors pause/resume/status through the same
application services. There is no control that opens a circuit breaker by
hand and no kill switch distinct from paper pause.

## 6. Configuration surfaces

| Surface | Storage | Examples |
|---|---|---|
| Bootstrap env (`internal/config`) | process env, first boot only for several | `ARB_MODE` (seed), `ARB_DATABASE_URL`, `ARB_HTTP_ADDR`, `ARB_RECORDING_DIR`, `ARB_SECRET_KEY`, admin bootstrap |
| Strategy parameters (`internal/strategy`) | `strategy_configs`, immutable versions, hot-swapped into the scanner | buffers 5+5 bps, TTL, `min_input`, depth, workers, `min_net_edge_bps` 5, `max_trade_size` 1000, utilization 0.5, concurrency 3, book age 1500 ms / spread 750 ms, `max_slippage_bps` 50, `max_price_impact_bps` 30, `max_daily_loss` 200, `max_drawdown` 0.05 |
| Platform settings (`internal/platform`) | `platform_settings`, versioned, restart-scoped except hot fields | symbols, starting assets, paper balances, venue fees (bps) and overrides, mode, log level, allowed origin, AI section, Telegram |
| Screener settings | `screener_settings` | venues enabled, poll interval, fee table, slippage allowances, auto-paper sizes |
| Simulation latency model | compiled constants in `app.Engine.Run` | submit 20 ms + U(0,30), fill 30 ms + U(0,50), limit tolerance 20 bps, depth 50 |

## 7. Data stores

Tables (migrations 1–15): `users, sessions, exchanges, exchange_health,
markets, triangles, opportunities, paper_sessions, paper_cycles, orders,
fills, virtual_balances, balance_snapshots, pnl_snapshots, strategy_configs,
ai_analyses, ai_recommendations, risk_events, alerts, notifications, reports,
audit_events, system_events, market_recording_metadata, campaign_runs,
replay_runs, platform_settings, secrets, screener_rules, screener_templates,
screener_settings, screener_events, funding_history, screener_paper_balances,
screener_paper_positions, screener_paper_executions, screener_reports,
organisations, memberships, subscriptions, billing_prices, paddle_events,
affiliate_accounts, affiliate_ledger, api_keys`. Raw market data is not stored
row-wise: the recorder writes compressed frame segments and registers only
metadata.

## 8. Where the map and the design disagree

The design documents (`docs/architecture.md`, `docs/data-flow.md`,
`docs/risk.md`) describe several controls that the mapping did not find in
code. They are recorded as findings, with evidence, in
`execution-risk-audit.md` and `market-data-audit.md`:

- revalidation of the three books between RESERVED and SIMULATING;
- circuit breakers opening on disconnect, sequence gap, stale feed, loss or
  drawdown limits, ledger invariant breach or persistence failure;
- daily-loss and drawdown inputs to the risk gate;
- fill-time book health checks in the paper executor;
- runtime verification of the reservation ledger invariants.
