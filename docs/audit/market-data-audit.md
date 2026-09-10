# Market-data and order-book audit

Audited tree: `master` at `abddb55` (2026-09-09). Scope: `internal/exchange`,
`internal/exchange/binance`, `internal/marketdata`, `internal/orderbook`, the
feed path of `internal/replay` and its `internal/backtest` delegate, plus
`docs/research/market-data.md` and `docs/architecture.md` §5–§6, §15–§16.
`go build ./...` clean; `go vet` silent on every in-scope package;
`go test -race -count=1` passes on `internal/orderbook`, `internal/exchange`,
`internal/exchange/binance`, `internal/marketdata`, `internal/replay`.

No P0 was found: no path was constructed by which the reconciliation/apply
logic produces a wrong price or an undetected corruption for Binance.

## Findings (ranked)

### M1 · P1 · Reconnect backoff never resets and gates the planned 24 h reconnect — recurring, worsening feed blackouts
- Evidence: `internal/exchange/binance/feed.go:114-133` declares `backoff := f.BackoffMin` once outside the session loop and only ever doubles it (`backoff = min(backoff*2, f.BackoffMax)`); the pre-emptive reconnect (`feed.go:172,197-198`, `return errors.New("pre-emptive reconnect before 24h cut")`) goes through the same failure branch. No test exercises `Feed.Run`/`session` reconnect timing.
- Impact: once `backoff` has ratcheted to `BackoffMax` (30 s, plus up to 15 s jitter) — about six reconnects, including the feed's own daily rollovers — every later planned rollover costs up to ~45 s of DISCONNECTED on every book, purely by policy. Books fail safe (triangles suppressed), so this is availability, not correctness; it does gap today's paper evidence and would carry into a live mode.
- Action: reset `backoff` to `BackoffMin` once a session has been stable for longer than `2 × BackoffMax`; treat the pre-emptive return as a distinct near-zero-delay case (ideally dial the replacement connection before tearing down the old one).
- Validation: a fake-dialer test asserting the reset after a stable session and a negligible delay after a pre-emptive return.

### M2 · P1 · Clock-skew control unimplemented; `ClockHealthy` is hard-wired true
- Evidence: `docs/research/market-data.md:88-96` and `docs/architecture.md:134-138` mandate a clock manager (`ServerTime` offset estimate; degraded clock ⇒ qualification suspended). `internal/exchange/binance/transport.go:100-102` implements `RESTClient.ServerTime`; `grep -rn "\.ServerTime("` → only the definition. `internal/scanner/scanner.go:88-90` documents `ClockHealthy` as "flipped by the clock manager; default healthy until wired"; the only writes are `Store(true)` at construction (`internal/app/engine.go:872`, `internal/backtest/backtest.go:245`). `internal/risk/engine.go:91` can therefore never observe false.
- Impact: a documented, mandatory live-mode control does not exist, and every stored risk decision reports `RISK_CLOCK_UNSAFE` as passed — false assurance in the evidence. Book-age staleness itself is unaffected (monotonic in-process timestamps).
- Action: a small clock-manager component polling `ServerTime`, tracking an RTT-bounded offset, driving `Scanner.ClockHealthy` down past a threshold; until built, mark the doc claim "not implemented".
- Validation: unit test the estimator; integration test that `ClockHealthy=false` yields `RISK_CLOCK_UNSAFE`.

### M3 · P2 · The STALE transition is never evaluated in the replay/backtest path
- Evidence: `EvaluateStaleness` (`internal/orderbook/book.go:161-170`) is called only by the live engine's wall-clock ticker (`internal/app/engine.go:1102-1108`); `internal/backtest/backtest.go` (which drives `marketdata.Replayer` under a `simulation.VirtualClock` and backs `internal/replay.Execute`) never calls it.
- Impact: in replay a quiet market never leaves HEALTHY, so reported `State` is wrong for that scenario and the STALE→CORRUPTED arc is never exercised. Materially backstopped: the scanner computes `ages` from the virtual clock and the risk gate's `MaxBookAge`/`MaxBookAgeSpread` are mandatory and bounded (`internal/strategy/params.go:144-145,165-168`).
- Action: sweep staleness inside the backtest harness once per simulated tick using `h.clock.Now()`.

### M4 · P2 · No crossed-book or non-positive-price integrity check at the order-book layer
- Evidence: `internal/orderbook/book.go:248-283` filters only on quantity; `internal/exchange/binance/decoder.go:107-121` parses without sign validation; `grep -rn "[Cc]rossed" internal/` → none in engine code. Downstream, `ValidateOrder` (`internal/exchange/instrument.go:120-123`) rejects a negative VWAP but not zero, and min-notional only catches a fully zero-priced fill. The convention exists elsewhere (`internal/portfolio/portfolio.go:276`, every `internal/screener/venue/*.go` collector).
- Impact: defence-in-depth gap; a venue-side anomaly would sit undetected rather than marking the book CORRUPTED.
- Action: O(1) check after every apply/snapshot (`bids[0].Price >= asks[0].Price` ⇒ CORRUPTED), filter non-positive prices like quantities, export `orderbook_crossed_total`.

### M5 · P2 · Per-market resync goroutines are not scoped to their session
- Evidence: `internal/exchange/binance/feed.go:170,235,244` spawn `fetchSnapshots`/`resyncMarket` with the process-lifetime `ctx`; `resyncMarket` (`feed.go:301-341`) retries up to ~31 s against a `*Syncer` captured at entry; only the connection is session-scoped (`defer conn.Close()`, line 143). The shared REST weight gate (`weight.go:26,42-76`) is then contended by stray old-session calls during the new session's resyncs.
- Impact: not a correctness bug (the registry resolves by market id to the current book), but it slows time-to-HEALTHY during exactly the reconnect-storm scenario the docs name.
- Action: derive a per-session context cancelled in `session`'s defer and pass it to both goroutines.

### M6 · P3 · Metrics book-age source uses `time.Now()` rather than the injected clock
- Evidence: `internal/app/engine.go:1338-1353`. Live-only today; inconsistent with the "Clock everywhere" principle.

### M7 · P3 · `docs/architecture.md` §4's `MarketDataConnector` (`SetSubscriptions`, `Health()`) is not implemented by `binance.Feed`
- Evidence: `Feed.Symbols` is baked into the stream URL once (`transport.go:118-126`); the engine rebuilds the feed on topology change instead. A legitimate design; the doc should say so.

## Verified correct

- Binance splice and steady-state validation implement exactly `U <= lastUpdateId+1 <= u`: `internal/exchange/binance/validator.go:23-34` (drop `u <= lastUpdateId`, gap on `U > lastUpdateId+1`) and `validator.go:109-142` (`Syncer.OnSnapshot` trims and rejects with `ErrSnapshotBehindBuffer`); pinned by `TestValidatorTable`, `TestSyncerOfficialSpliceFlow`, `TestSyncerSnapshotBehindBuffer`.
- Gaps force a real resync: `Book.Apply` routes `ActionGap` to `MarkCorrupted` (`book.go:98-115`); `Syncer.OnDelta`/`OnSnapshot` flip to SYNCING, clear the buffer and require a fresh snapshot (`TestStateMachineTransitions`).
- Chaos suite present and passing: duplication flood, message loss, out-of-order delivery, snapshot delay, REST failure during resync, WS freeze, clock skew (age sanity), reconnect storm, burst (`internal/exchange/binance/chaos_test.go`).
- Decimal-preserving decode end to end; `decimal.DivisionPrecision` raised to 28 module-wide (`internal/exchange/decimal.go`).
- Absolute-quantity level semantics including delete-absent no-op (`book.go:266-283`, `TestDeltaMergeReplaceInsertDelete`, `TestDeleteAbsentLevelIsNoop`).
- Single writer per book: all mutations reach the book only through `Syncer` serialised by `Syncer.mu`; `MarkDisconnected` runs after the session's frame loop has stopped; `-race` clean, `TestSyncerConcurrentDeltaAndSnapshotIsRaceFree`, `TestResyncSingleFlightPerMarket`.
- `View()` returns an immutable copy under `RLock` (`book.go:219-231`, `copyTop`); `TestViewIsolation`, `TestConcurrentApplyAndView`.
- No lock held across I/O or logging on the hot path.
- Queues: the WS frame handoff (cap 1024) deliberately back-pressures rather than drops (dropping a raw depth frame would manufacture a gap); the recording tap drops non-blocking with a counter (`internal/marketdata/recorder.go:83-89`); sequence/resync/reconnect counters are atomics scraped asynchronously.
- Replay determinism for decode→validate→apply: `internal/marketdata/replay.go` reuses the live decoder/syncer/book; `TestReplayDeterminism` asserts identical fingerprints and action logs; the backtest drives a virtual clock from recorded receive timestamps and wires it into the scanner.
- REST weight budgeting: sliding 60 s window under a 4500/6000 ceiling, `Retry-After` honoured for 429/418, single-flight resync per market (`weight.go`).
- 24 h forced disconnect pre-empted at 23 h; server pings answered with a 75 s read deadline (`feed.go:105-107,146-152,172,197-198`).
- Book versioning cited per leg (`book.go:90,128`; `pricing.LegQuote.BookVersion`).
