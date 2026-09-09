# Performance plan

Audited tree: `master` at `abddb55` (2026-09-09). Measurements below were
taken in the audit environment with the repository's own benchmarks and
are indicative, not production numbers.

## Measured

| Benchmark | Result |
|---|---|
| `internal/pricing BenchmarkQuoteCycle50Levels` | 138 032 ns/op |
| `internal/pricing BenchmarkSizeSearch50Levels` | 7 250 997 ns/op, 3 641 949 B/op, 108 231 allocs/op (41 evaluations) |
| `internal/metrics` atomic add | ≈ 7.8 ns, 0 allocs |
| `internal/metrics` unlabelled histogram record | ≈ 135 ns, 0 allocs |
| `internal/metrics` labelled record (precomputed attributes) | ≈ 273 ns, 1 alloc |
| `go test -race ./...` | ≈ 5.5 min wall (`internal/api` 315 s) |

## Hot path today

WS frame → decode → `Syncer` → `Book.Apply` (single writer) → `Set.MarkDirty`
(coalesced) → worker pool (default 2) → `EvaluateTriangle` → `SizeSearch`
(13-point grid + 14 ternary iterations, each a full `QuoteCycle`) →
`risk.Evaluate` → bounded event channel. Verified free of database, AI,
Telegram and network calls; metrics are atomics or off-path histograms;
book views are copied under `RLock`; no lock is held across I/O.

## Findings and plan

1. **Size search cost (trading T11).** ≈7.3 ms and 108 k allocations per
   affected triangle against a "sub-millisecond" comment; with two workers,
   a dirty market touching N triangles costs N × 7 ms before the next
   frame is evaluated. Plan: breakpoint-derived candidates (also fixes T1),
   pre-sized slices, constants hoisted out of loops, leg-1 budget walk
   cached across adjacent candidates; pin ns/op and allocations in CI.
2. **Reservation mutex on the evaluator path.** `Capital.Balance` and
   `TriangleReserved` take the single ledger mutex per evaluation
   (`scanner.go:230,240`). Plan: publish an atomic snapshot of
   available/reserved per asset on every transition and read it lock-free.
3. **`Registry.AnyOpen` allocates a map per evaluation** (`breaker.go:144`).
   Plan: cache open scopes in an atomic value updated on transitions.
4. **Dispatch gap unmeasured.** `triangle_evaluation_duration` starts at
   `EvaluateTriangle` entry; the signal → drain → channel → worker gap is
   invisible. Plan: stamp the dirty-mark time and record queueing delay.
5. **Latency chain not exported** (observability O3). Plan: submission,
   ack, execution and cycle-completion histograms; p50/p95/p99 panels.
6. **Screener hot path** (scanner X9): per-event full ledger scans
   (`ListExecutions` limit 0 → 20 000 rows), per-request ledger re-reads,
   real sleeps inside the automation tick. Plan: incremental drift per
   lane, per-tick view cache, virtual waiter for paper.
7. **Database** (database D7/D8): missing `(session_id, started_at DESC)`
   index and symbol/triangle filters on orders/fills; one 8-connection pool
   shared by the outbox writer and the API with no statement timeout. Plan:
   the two indexes, `statement_timeout`, reserved outbox capacity.
8. **Test-suite wall time.** `internal/api` at 120–315 s serial; no
   `t.Parallel`. Plan: parallelise independent handler tests behind
   per-test servers.

## Measurement before optimisation

Every item above lands with a benchmark or metric first (the repository
already has `scripts/bench.sh` and `docs/benchmarks.md`); no optimisation
is accepted without a before/after number.
