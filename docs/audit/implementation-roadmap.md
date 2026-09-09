# Implementation roadmap — consolidated backlog

Consolidated from the specialist audits in this directory (`master` at
`abddb55`, 2026-09-09). Each item names the source finding so the evidence
can be re-read. Severity: P0 critical, P1 high, P2 medium, P3 low. Order
within a band follows the implementation order in `master-report.md`
(catastrophic correctness → execution safety → financial calculation →
market data → risk controls → concurrency → reliability → security →
observability → architecture → UX → polish → performance).

## P0 — Critical

| # | Item | Source | Fix shape |
|---|---|---|---|
| P0-1 | Slippage is measured against the buffered estimate at the planned size; partial fills produce values in the thousands of bps; persisted and scored | execution F1, trading T3 | baseline `Quote.FinalAmount` at the deployed size; persist the size ratio separately; fix the test that pins −5 bps |
| P0-2 | Realized and unrealized PnL conflated in opposite directions (portfolio books the full input as a loss on a mid-cycle failure; the cycle row stores the marked total as `pnl_amount`, summed as realized by the quality score) | execution F9 | persist `realized_pnl` and `exposure_mark` separately; report cash-basis and marked figures side by side; score quality on realized |
| P0-3 | Financial records can be lost silently: outbox write failures uncounted, paper-queue drops uncounted, persistence hook never wired, shutdown cancels the outbox before in-flight cycles settle, a dropped opportunity row cascades into a lost cycle row via the FK | database D3, observability O1, execution F7/F15 | counters + metrics, wire `OnPersistError` to a breaker and CRITICAL alert, staged shutdown with an `ABORTED` outcome, unlinked (NULL `opportunity_id`) cycle insert when the opportunity row is missing |

### P0 status

| # | Status | Landed as |
|---|---|---|
| P0-1 | Fixed | `slippageVsPlan` in `internal/simulation/paper.go`: planned and actual returns per unit of deployed input; `planned_return_bps` / `actual_return_bps` persisted; partial fills no longer report thousands of bps |
| P0-2 | Fixed | `realized_pnl`, `exposure_mark`, `input_consumed`, `final_amount` on `paper_cycles` (migration 000016); quality score sums realized; `Portfolio.NetPnL` / `DailyLoss` net the marked exposure; API PnL rows carry `realized`, `exposure_mark`, `net_pnl`, `unmarked` |
| P0-3 | Fixed | `storage.Outbox`: write failures and drain-deadline losses counted, closed state after shutdown, recovery probe; global `persistence` breaker trips on a failed write and closes on recovery; paper-queue drops counted (`paper_queue_dropped_total`, health `queues.paper.dropped`); `Engine.shutdownStaged` cancels the outbox only after the producers (paper engine included) return; cancellation mid-cycle settles as `ABORTED`, deadline as `TIMEOUT`; `InsertCycle` writes an unlinked cycle (NULL `opportunity_id`, counted as `unlinked_cycles`) instead of failing the FK |

## P1 — High

| # | Item | Source |
|---|---|---|
| P1-1 | Circuit breakers are never tripped by anything; breaker board, alert and metric permanently green | execution F2, observability O2 |
| P1-2 | Daily-loss and drawdown limits never reach the risk gate | execution F3 |
| P1-3 | No revalidation of books or risk before leg 1 | execution F4 |
| P1-4 | Paper fills ignore book health at fill time | execution F5 |
| P1-5 | Ledger invariants never verified at runtime | execution F6 |
| P1-6 | Restart resets the paper ledger, PnL, exposure and drawdown; PnL history tables are dead schema | execution F8, database D4 |
| P1-7 | Reconnect backoff never resets and gates the planned 24 h reconnect | market data M1 |
| P1-8 | Clock manager unimplemented; `RISK_CLOCK_UNSAFE` dead | market data M2, observability O4 |
| P1-9 | Size search can miss the profitable size at large `max_trade_size / min_input` ratios | trading T1 |
| P1-10 | Login throttling keys on the proxy address: unauthenticated global lockout | security S1 |
| P1-11 | `smtp_url` credential reaches logs, database and an API response through a parse error | security S2 |
| P1-12 | API keys survive account disable and membership removal | security S3 |
| P1-13 | Tenant isolation never engages (platform membership always wins); screener settings and nightly reports are platform-global and writable/readable by any organisation | security S4/S7, database D1/D2 |
| P1-14 | Bootstrap admin re-upserted on every boot (password, role, status) | security S6, infra I2 |
| P1-15 | Backups and restore drill cannot run against the declared managed database; drill writes to production | infra I1 |
| P1-16 | Documented compose deployment publishes Postgres with a default password on all interfaces | infra I3 |
| P1-17 | Production canary cannot pass and would run a second engine on the production database | infra I4 |
| P1-18 | No retention job; `cmd/worker` builds no components; tables grow forever | database D5, infra I9 |
| P1-19 | Audit/risk-event immutability enforced by nothing | database D6, security S9 |
| P1-20 | Binance USDT and USDC perpetuals collide on one `(venue, base)` key; funding history mixes contracts | scanner X1 |
| P1-21 | Evaluator lane universe truncated to the 500 highest-net rows with suspect lanes ranked first | scanner X2 |
| P1-22 | Data-age gate plus unsynchronised poll/tick phases resets lifetimes and closes events; close reason not stored | scanner X3 |
| P1-23 | Order/cycle latency chain never exported; rejections and outcomes have no reason/outcome labels | observability O3/O5 |
| P1-24 | Console: mode banner absent on mobile; no emergency control (pause is 2–3 clicks, unconfirmed, generic failure copy); fee-inclusive figure labelled "Gross"; cycle-outcome vocabulary mismatch; overview fails the five-second test; Paper page is not a live-cycle monitor; request failures rendered as empty states; float division on a persisted threshold | ui F1–F8 |

### P1 status

| # | Status | Landed as |
|---|---|---|
| P1-1 | Fixed | Breaker policies in `internal/app/riskpolicy.go`: `feed_instability` (exchange scope) opens on 5 coalesced book faults per minute, probes after 30 s and closes after a quiet probe window; `simulation_inconsistency`, `daily_loss`, `drawdown` (global) open on their triggers and stay open until an operator closes them; `persistence` (P0-3). Book transitions reach the policy through `orderbook.Set.OnTransition`; the scanner gate also honours `market:` scopes |
| P1-2 | Fixed | `scanner.LedgerView` (session loss and drawdown per start asset) feeds `risk.Context.DailyLoss/Drawdown`; the engine's `sessionLedger` refreshes after every settlement and on the tick, which also advances the portfolio's high-water mark |
| P1-3 | Fixed | `scanner.Revalidate` re-quotes a qualified plan at its size on the current books (only when a book version moved) and re-runs the full risk gate; the paper engine calls it before reserving capital and skips, counts and reports refusals (`opportunities_revalidation_rejected_total`, risk_events) |
| P1-4 | Fixed | `simulation.fillLeg` requires a HEALTHY fill-time book and honours `Config.MaxBookAge` (live paper: the scanner's 2 s budget); an unhealthy leg-1 book is REJECTED, a later one strands exposure like any other mid-cycle failure |
| P1-5 | Fixed | The paper engine runs `reservation.CheckInvariants` after every settlement and release, pauses itself on a violation, counts it (`invariant_violations`) and opens the `simulation_inconsistency` breaker |
| P1-6 | Fixed | A ledger snapshot (cash per start asset, realized, peak, drawdown, fees, exposure, counters) is written through the outbox after every settlement into `virtual_balances`, `balance_snapshots` and `pnl_snapshots`; a restart resumes the latest open paper session from its snapshot when the configured starting balances still match (a changed configuration or a paper reset ends the session instead); the supervisor no longer ends the outgoing session on restart |
| P1-7 | Fixed | `binance.Feed` resets its backoff after a session that stayed up for `StableAfter` (60 s) and reconnects with zero delay on the pre-emptive 24 h rollover (`ErrPreemptiveReconnect`) |
| P1-8 | Fixed | `marketdata.ClockMonitor` polls the venue's server time (RTT-halved offset, 30 s interval, 500 ms limit, 3 consecutive failures → unhealthy); the engine feeds `scanner.ClockHealthy` from it every tick, so qualification starts only after the first successful probe; health reports `clock.healthy`, `clock.offset_ms` |
| P1-23 | Fixed | `order_latency_ms{exchange,stage}` (submit_ack, ack_fill, submit_fill) and `cycle_duration_ms{exchange}` histograms recorded per settled cycle; `risk_rejections_total{exchange,stage,reason}` (qualification and revalidation), `paper_cycle_outcomes_total{exchange,outcome}` and `order_outcomes_total{exchange,status}` replace the flat aggregates; `paper_cycles_received_total` / `paper_cycles_skipped_total` exported (O6) |

## P2 — Medium

- Sizer objective ignores the impact cap and minimum edge (trading T2); `MARKET_LOT_SIZE` not parsed (T4); limit prices not tick-quantized (T5); `ValidateOrder` on filled rather than submitted quantity (T6); fee rates hard-coded rather than fetched from the venue (T7 — P1 for any non-VIP0 account); topology and rules never refreshed (T8); `MaxSlippageBps` never evaluated (T9, execution F12); constraint duplication in the screener (T10); sizer ~7× its documented latency budget (T11).
- Duplicate ACTIVE reservation key accepted (execution F10); `max_concurrent_simulations` not honoured (F11); non-start-asset fees unvalued (F13); cycle row lacks expected-vs-actual (F14); optimistic exposure mark and no unwind (F16).
- Replay never evaluates STALE (market data M3); no crossed-book / non-positive-price check (M4); resync goroutines outlive their session (M5).
- Console RBAC lacks an organisation dimension; VIEWER reads platform settings, secrets inventory, config and `/metrics` (security S5); provider-group secrets writable by a promoted ADMIN (S8); member add without consent and roster disclosure (S10); WebSocket topics unauthorised (S11).
- SLO bake gate fails open (infra I5); strategy params fall back to defaults silently (I6); migrations without lock/statement timeouts (I7); ExternalSecret hook lifecycle (I8); recorder disk-full invisible (I10); alert blind spots and label mismatch (I11); unused Redis (I12); CI/CD hardening and non-executable deploy pipeline (I13).
- Missing indexes (database D7); shared small pool without timeouts (D8); unbounded funding query (D9); no CHECK constraints on financial columns (D10).
- Funding attribution shifted one interval on previous-period venues (scanner X4); restart loses evaluator state (X5); reports omit open positions (X6); book never evicts (X7); perp legs have no depth (X8); hot-path cost scales with ledger size and real sleeps in the tick (X9); Coinbase 429 handling (X10); calculator without age/guard (X11).
- `paper.Stats.Received/Skipped` not exported (observability O6); slippage unpanelled (O7); queue depths not exported (O8); no realization ratio (O9); screener venue health unpanelled (O10); no spans (O11).
- Console: no data-age indicator on polled tables (ui F9); opportunities list gaps (F10); strategy parameters without units/defaults (F11); Risk Center gaps (F12); no cycle detail route (F13); Scanner net bps always green (F14); accessibility (F15); tooltips hiding decision text (F16); navigation dead ends (F17); duplicated helpers and one contrast failure (F18).

## P3 — Low

- Trading precision cluster (T12): min-notional on exact cost, `NOTIONAL.applyMinToMarket`, `Usable()` without a `NOTIONAL` filter, `DepthExhausted` from the budget walk, dead `fees.Bps`, discount refusal location, 1e-28 clamp.
- Execution dead code and duplicated paper stack (F17); metrics `time.Now()` in the book-age source (M6); `MarketDataConnector` doc drift (M7).
- Argon2 parameter clamp (S12); HTTP server timeouts (S13); CI pinning and `npm audit` (S14); marketing-site headers and markdown sanitiser (S15).
- Log/trace shipping mismatch (I14); configuration residue (I15); restore-drill table and stale runbook (D11).
- Scanner reservation error handling (X12), funding edge cases (X13), asset identity heuristics (X14), dead code (X15).
- Stale rules header (O12), `triangles_total` naming (O13), AI/Telegram panels (O14), `replay_runs_total` (O15).
- Console responsive grids (ui F19) and primitive bypasses (F20).

## Sequencing for this branch

1. P0-1, P0-2, P0-3 with tests.
2. Execution safety: P1-3, P1-4, P1-5, P1-1 (feed, loss, invariant, persistence triggers; paper gate), P1-2.
3. Financial calculation: P1-9, T3 is covered by P0-1.
4. Market data: P1-7.
5. Security: P1-10, P1-11, P1-12, P1-14; the tenancy fix in P1-13 that is safe without a product decision (prefer the non-platform membership; gate platform-global screener settings).
6. Observability: P1-23 and the counters from P0-3.
7. Scanner: P1-20 (USDT filter), then P1-21/P1-22.
8. Console: P1-24 items F1, F3, F4, F7, F8, then F2.
9. Infra: P1-16 and the canary chart assertion from P1-17; P1-15, P1-18, P1-19 need operator decisions and are documented rather than changed here.
