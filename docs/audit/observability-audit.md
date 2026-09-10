# Observability and latency-instrumentation audit

Audited tree: `master` at `abddb55` (2026-09-09). Scope: `internal/metrics`,
`internal/app`, `internal/api`, the hot-path packages, and
`deploy/observability`. `go build`, `go vet` and `go test ./internal/metrics/...
-bench=.` are clean; the benchmarks reproduce the figures documented in the
code (atomic add ≈ 7.8 ns / 0 allocs; unlabelled histogram record ≈ 135 ns /
0 allocs; labelled record with a precomputed attribute set ≈ 273 ns / 1 alloc).
Exposed names below are the actual `/metrics` series names (the OTel
Prometheus exporter appends `_total` to counters and `_bucket/_sum/_count`
to histograms).

## Metric inventory

| Exposed name | Type | Labels | Source |
|---|---|---|---|
| `triangle_evaluation_duration_*` | histogram | – | `scanner.EvalObserver` → `metrics.go:100-102` |
| `api_request_duration_*` | histogram | method, route, status | `metrics.go:105-111` |
| `market_message_latency_ms_*` | histogram | exchange | exchange event time → local receive (`binance/feed.go:212-225`) |
| `net_edge_bps_*` | histogram | exchange | qualified opportunities (`engine.go:1174-1176`) |
| `slippage_bps_*` | histogram | exchange | `engine.go:951` (ALL_FILLED only) |
| `triangles_evaluated_total`, `opportunities_detected_total`, `opportunities_qualified_total`, `opportunities_rejected_total`, `scanner_skipped_unhealthy_total`, `scanner_dropped_events_total` | counters | – | `scanner.Stats` (`metrics.go:278-283`) |
| `triangles_total` | gauge | – | topology size |
| `market_messages_total`, `exchange_reconnects_total`, `exchange_api_errors_total`, `orderbook_resync_total`, `orderbook_sequence_errors_total` | counters | exchange | `binance.Feed.Stats` |
| `orderbook_age_ms`, `orderbook_state` | gauges | exchange, market | book views (`metrics.go:297-304`) |
| `capital_available`, `capital_reserved` | gauges (display float) | asset | reservation manager |
| `circuit_breaker_state` | gauge (0–2) | name, scope | `risk.Registry.States()` |
| `paper_cycles_total`, `paper_cycles_success_total`, `paper_cycles_failed_total`, `paper_active_simulations` | counters/gauge | – | `paper.Stats` (`metrics.go:320-327`) |
| `paper_pnl`, `fees_total` | gauge / counter (display float) | asset | portfolio |
| `recorder_frames_written_total`, `recorder_frames_dropped_total` | counters | – | recorder control |
| `ai_requests_total`, `ai_failures_total`, `telegram_messages_total`, `telegram_errors_total`, `websocket_clients` | counters/gauge | – | components |
| `exchange_rate_limited_total` | counter | venue | screener collectors + engine 429/418 merged under `venue="binance"` |
| `screener_venue_online`, `screener_poll_latency_ms`, `screener_pairs_tracked`, `screener_alerts_total{rule_kind}`, `screener_paper_executions_total{strategy,outcome}` | gauges/counters | as shown | `metrics_screener.go` |
| `db_migrations_pending`, `campaign_runs_total{status}` | gauge / counter | – / status | components |

Every registration is nil-guarded and every source field is skipped when nil (`TestNilSourcesAreSkipped`, `TestScreenerNilSourcesAreSkipped`).

## Required-set comparison

| Required | Status | Note |
|---|---|---|
| market data messages, age, exchange→receive latency | PRESENT | |
| decision latency (receive → qualified) | PARTIAL | `triangle_evaluation_duration` starts at `EvaluateTriangle` entry (`scanner.go:172-176`), after the dirty-signal → drain → work-channel → worker dispatch; the dispatch gap is unmeasured |
| order submission / ack / execution / cycle-completion latency with p50/p95/p99 | MISSING | timestamps exist (`SimOrder.CreatedAt/AckedAt/FilledAt`, `CycleResult.StartedAt/SettledAt`); `orders.latency_ms` is even persisted (`records.go:203-205`); no histogram |
| opportunities detected / qualified | PRESENT | |
| opportunities rejected by reason | PARTIAL | aggregate only; the per-reason map lives in memory (`engine.go:168-178,250-256`) and the risk API |
| cycles started / completed / failed | PRESENT | |
| cycles unwound / aborted | MISSING | no unwind or ABORTED concept; pre-execution skips fold into `paper.Stats.Skipped`, which never reaches Prometheus |
| order rejections, partial fills | MISSING | per-order in Postgres only; partial outcomes count as successes |
| realized PnL | PRESENT | `paper_pnl{asset}` |
| expected PnL, realization ratio | MISSING | `NetProfit`/`EstimatedFinal` never aggregated; no realization ratio anywhere in the repository |
| slippage | PRESENT but unused | zero dashboard panels, zero alert rules |
| exchange API errors, reconnects, sequence gaps | PRESENT | |
| outbox drops, queue depths | MISSING from Prometheus | counters exist in code, surfaced only in the health JSON |
| breaker state | PRESENT but dead | nothing trips a breaker in production |
| screener poll latency / rate limits | PRESENT | no dashboard or alert for venue offline/slow |

## Findings (ranked)

### O1 · P1 · Silent, largely uncounted drop points between "qualified" and "persisted"; the persistence-failure hook is never wired
- Evidence: `internal/app/engine.go:1191-1197` drops a qualified opportunity when the paper queue is full with only a WARN log; `internal/storage/outbox.go:49-58` counts channel-full drops but `write()` (`:112-137`) logs a Postgres error and returns without incrementing anything; `OnPersistError` (`outbox.go:23-24`) is never assigned (`engine.go:726`); `Outbox.Dropped()` reaches only the health JSON (`readmodel.go:165-169`), never a metric.
- Impact: during a Postgres outage or a paper backlog, qualified opportunities and settled cycles — the evidence the platform exists to produce — can be lost with no metric, alert or breaker. (Rated P1 here rather than P0 because paper evidence, not money, is lost; see `database-audit.md` D3 and `execution-risk-audit.md` F7/F15.)
- Action: `outbox_dropped_total`, `outbox_write_failed_total`, `paper_queue_dropped_total`, `outbox_queue_depth`/capacity gauges; wire `OnPersistError` to a `persistence` breaker.
- Validation: stop Postgres during a paper run and watch `/metrics` (no new series today) versus the logs.

### O2 · P1 · The breaker subsystem is fully instrumented and never triggered
- Evidence: `internal/risk/breaker.go:85`; `internal/app/engine.go:827-848` builds the observer; `grep -rn "\.Trip(" internal cmd` outside tests and the definition → nothing; same for `Probe`/`Close`. `circuit_breaker_state` is pinned at 0, the `CircuitBreakerOpen` alert (`prometheus-rules.yml:55-60`) and the "Breakers OPEN" panel can only show green.
- Action: wire real triggers (see `execution-risk-audit.md` F2) or remove the metric/alert/panel until connected.

### O3 · P1 · The order/cycle latency chain is captured as timestamps and never exported
- Evidence: `execution/executor.go:82-84,120-121`; `simulation/paper.go:159,182,192,208,302`; `storage/records.go:203-205` persists `latency_ms`; `internal/metrics/metrics.go` has five histograms, none for submission/ack/execution/cycle completion.
- Impact: no p50/p95/p99 for the one number that calibrates the latency buffer.
- Action: four histograms recorded in `paper.Engine.runCycle`/`simulation.Engine.settle`, following the existing off-hot-path pattern.

### O4 · P1 · Clock manager unimplemented; `RISK_CLOCK_UNSAFE` is permanently dead and no health field reports clock quality
- Evidence: `scanner.go:88-90`, `engine.go:872`, `risk/engine.go:45,91`; `grep -rln "ClockManager\|ClockQualityChanged"` → nothing; `docs/architecture.md:448`, `docs/data-flow.md:77` document the control. (Same finding as `market-data-audit.md` M2.)
- Action: implement a minimal offset poller, or expose "not implemented" in the health payload.

### O5 · P1 · Rejected opportunities and paper-cycle outcomes exported as flat aggregates
- Evidence: `metrics.go:243,281` (no reason attribute); `simulation/paper.go:221-241` and `paper/engine.go` collapse nine outcomes into completed/failed/skipped, and `Skipped` is not exported (O6).
- Impact: "risk engine is blocking on drawdown" and "books were briefly stale" look identical; partial fills and rejections have no signal.
- Action: bounded `reason` label (≈17 values) on the rejected counter; bounded `outcome` label on the cycle counters.

### O6 · P2 · `paper.Stats.Received` and `Skipped` are built for the metrics source and discarded
- Evidence: `engine.go:1394-1405` fills `metrics.PaperStats` completely; `metrics.go:320-327` observes only `Started/Completed/Failed/Active`.
- Action: add the two `ObserveInt64` calls, or break `Skipped` out by cause.

### O7 · P2 · `slippage_bps` has no panel and no alert
- Evidence: implemented and tested (`metrics.go:87-90,134-137`, `metrics_test.go:69,91`); `grep -n slippage deploy/observability/*` → nothing.
- Action: p50/p95 panel mirroring the net-edge heatmap; a "slippage exceeds buffer" alert.

### O8 · P2 · Queue depths only in the health JSON
- Evidence: `outbox.go:67-75`, `paper/engine.go:76-88`, `readmodel.go:160-179`; `EngineSources` (`metrics.go:191-201`) has no queue field.
- Action: `outbox_queue_depth`, `paper_queue_depth` (+ capacity) gauges using the `orderbook_age_ms` pattern.

### O9 · P2 · No realization ratio anywhere
- Evidence: repo-wide search for realization/realized-vs-expected → nothing; `internal/quality` computes an unrelated composite.
- Action: `sum(RealizedPnL) / sum(EstimatedFinal − InputConsumed)` from `CycleResult` and `Opportunity`, exposed as a gauge and in the reports.

### O10 · P2 · Screener venue-health metrics have no panel and no offline/slow alert
- Evidence: `metrics.go:452-516`, `metrics_screener.go:24-59`; `deploy/observability/README.md:26-30,52-58` lists dashboard coverage without them; rules alert only on rate limiting.

### O11 · P2 · No OTel span is created anywhere; tracing is backend infrastructure only
- Evidence: `grep -rn "otel/trace\|StartSpan\|tracer\.Start" internal cmd` → nothing; `tempo-values.yaml:1-4` says so. Log correlation by `opportunity_id`/`cycle_id`/`triangle_id` works today.

### O12 · P3 · `platform-rules.yml:5-10` header marks three metrics "PENDING EXPORTER" that are implemented and wired
- Evidence: `exchange_rate_limited` (`metrics.go:470`), `db_migrations_pending` (`:522-523`), `campaign_runs` (`:537-538`); asserted by `metrics_test.go:210,220-222`.

### O13 · P3 · `triangles_total` is a gauge with a counter's suffix
- Evidence: `metrics.go:246` (`i64g`); `rate()` on it would be nonsense. Rename at the next breaking window.

### O14 · P3 · AI and Telegram counters have no panels or alerts; O15 · P3 · no `replay_runs_total{status}` counterpart to `campaign_runs_total`.

## Verified correct

- Hot-path cost is benchmarked, not asserted; no network call inline in `internal/metrics`; no unbounded label on any of the 43 series (`opportunity_id`/`cycle_id`/`order_id`/`triangle_id` never used as labels).
- No per-tick or per-evaluation logging: only WARN/ERROR on abnormal events in `internal/scanner`, `internal/exchange/binance`, `internal/orderbook`; "opportunity qualified" at INFO with ids, "rejected" at DEBUG (`engine.go:1199-1222`).
- Runtime log level via `slog.LevelVar` swapped on every settings change (`internal/app/logging.go:18-23,73-81`).
- Secret redaction layered and tested: key-based redaction (`logging.go:13-16`), `Bootstrap.Redacted()` with a reflective tripwire test (`config_test.go:82-106`), Telegram token split out of the base URL and stripped from errors (`telegram/client.go:23-27,45-53`), pgx redacts the DSN password in its own error strings. (The `smtp_url` parse-error path in `security-audit.md` S2 is the exception.)
- Liveness and readiness separated: `/healthz` pure liveness; `/readyz` checks the pool and deliberately not feed health (`server.go:276-277`); system health aggregates goroutines, heap, GC, pool stats, feed/book state and queue depths.
- Replay never pollutes latency histograms with wall-clock reads: backtest wires the harness clock and attaches no `Metrics`.
- Every PromQL expression in both dashboards and both rule files references an existing series (the one mismatch is a label name, `infra-delivery-audit.md` I11).
- The Binance client is public-market-data only; no signing or key exists on that path.
