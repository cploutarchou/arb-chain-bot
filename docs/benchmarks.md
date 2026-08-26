# Performance benchmarks & budgets (T-044, SKILL §73)

Reproduce with `scripts/bench.sh`. Baselines recorded on the dev
container (Intel Xeon @2.80GHz, 4 vCPU, Go 1.24, `-benchtime=1s`);
budgets are ceilings with headroom over the baseline — a bench landing
above its budget is a regression to investigate, not to accept.
Re-record baselines only on hardware changes, never to absorb a
regression.

| Benchmark (§73 item) | Baseline | Budget | Notes |
|---|---|---|---|
| `orderbook.BenchmarkApplyDelta` (book application) | 432 ns/op | 2 µs | single-writer path per delta |
| `orderbook.BenchmarkView50` | 1.7 µs/op | 8 µs | 50-level copy under RLock |
| `binance.BenchmarkDecodeWSFrame` (book application) | 9.0 µs/op | 30 µs | JSON envelope + decimal levels |
| `graph.BenchmarkBuildTopology` (startup only) | 0.73 ms/op | 10 ms | 123 markets, full rebuild |
| `pricing.BenchmarkQuoteCycle50Levels` (cycle conversion) | 95 µs/op | 300 µs | one exact 3-leg walk |
| `pricing.BenchmarkSizeSearch50Levels` (optimal size) | 7.9 ms/op | 25 ms | 13-pt grid + 14 ternary iters ≈ 80 quotes |
| `scanner.BenchmarkEvaluateMarket` (triangle recalculation) | 2.4 ms/op | 10 ms | 2 triangles × full pipeline per dirty market |
| `opportunity.BenchmarkBuild` (opportunity creation) | 3.7 µs/op | 20 µs | buffered economics |
| `opportunity.BenchmarkOpportunityJSON` (serialization) | 24 µs/op | 100 µs | outbox/hub write path |
| `simulation.BenchmarkExecuteCycle` (depth simulation) | 94 µs/op | 500 µs | 3 legs, virtual clock (no waits) |
| `realtime.BenchmarkPublishFanout100` (WS fan-out) | 32 µs/op | 150 µs | 1 publish → 100 sinks |
| `metrics.BenchmarkHotPathAtomicAdd` | 7.5 ns/op | 50 ns | per-frame counter cost |
| `metrics.BenchmarkEvalHistogramRecord` | 135 ns/op | 1 µs | per-evaluation observer |

Reading the numbers:

- The hot path budget story: a depth delta costs decode (9 µs) + apply
  (0.4 µs) + dirty-mark; a dirty market re-prices its triangles at
  ~2.4 ms dominated by the size search (~7.9 ms worst-case per
  triangle when depth allows large sizes). At the current 6-market /
  2-triangle scope that keeps end-to-end reaction well under 10 ms.
  Scaling the market set multiplies the EvaluateMarket cost by
  affected-triangle count — the first knob is `scanner.grid_points`.
- The size search dominates evaluation cost (~80 QuoteCycle calls).
  Its evaluation budget (grid + refine ≤ 60 quotes asserted in tests)
  is the tuning point if p99 evaluation latency ever matters more than
  sizing precision.
- `pprof`: `go test -bench BenchmarkEvaluateMarket -cpuprofile cpu.out
  ./internal/scanner/ && go tool pprof cpu.out`.
- The race detector runs in CI on the full suite (`go test -race`);
  benches run without `-race` (instrumentation skews timing).
