# Scanner Suite audit — cross-venue signals and automatic paper execution

Audited tree: `master` at `abddb55` (2026-09-09). Scope: `internal/screener`
(book, rules, settings, spreads, basis, calculator, guard), `internal/
screener/venue` (15 public-data collectors, rate gate, poller), `internal/
screener/alerts`, `internal/screener/paperexec`, `internal/screener/report`,
`internal/api/screenerapi.go`, `screenerreportsapi.go`, storage adapters,
migrations 000010–000012, app wiring, and the three design/research
documents. `go build`/`go vet` clean; `go test` passes on every package
(`venue` 23.8 s, `api -run Screener` 14.2 s); the env-gated live soak was
not run and no venue was contacted.

No signal or paper fill was found priced on the wrong side, with the wrong
fee, or with a report that labels a pre-fee figure as profit. The P1s are
about which lanes get evidence at all, and about one venue whose perp slot
can hold the wrong contract.

## Findings (ranked)

### X1 · P1 · Perp store keyed `(venue, base)`; Binance lists USDT and USDC perpetuals on the same base, so one overwrites the other and funding history mixes both
- Evidence: `internal/screener/book.go:63` `b.perps[PerpKey{Venue, Base}] = p`; `internal/screener/venue/binance.go:134-138` keeps every `contractType == "PERPETUAL"` with `Quote: s.QuoteAsset` and no quote filter (every other collector filters to USDT: `okx.go:115`, `bybit.go:119`, `mexc.go:162`, `kucoin.go:184`, `bingx.go:163`, `whitebit.go:200`, `bitmart.go:188`). The recorded fixture `internal/screener/testdata/binance/fut_premiumIndex.json` holds 5 USDC contracts among 55; `AAVEUSDT` precedes `AAVEUSDC`, so after `poller.go:217-219` the `(binance, AAVE)` slot holds the USDC contract (order-dependent). `poller.go:254` keys funding history the same way.
- Impact: bases whose slot is taken by the USDC contract carry `Quote: "USDC"` and are dropped by `alerts/signals.go:314-320` for a USDT rule — the lane never exists, silently shrinking the carry universe measured in T-097/T-100; `funding_history(binance, <base>)` receives the USDC contract's rates, which `signals.go:488-527` and `paperexec/perp.go:522-547` then read for USDT positions; `executor.go:341-351` and `perp.go:275` mark and close an open USDT position against whichever contract occupies the slot.
- Action: filter Binance perps to `quoteAsset == "USDT"`; key `Book.perps` and `funding_history` by `(venue, base, quote)`.
- Validation: poll the recorded premium index through the Poller and assert `PerpFor(binance, "AAVE").Quote == "USDT"`.

### X2 · P1 · The evaluator's lane universe per rule is truncated to the 500 highest-net rows, and suspect/stale lanes rank first
- Evidence: `internal/screener/alerts/signals.go:236,241` (`Limit: 500`, `IncludeSuspect: true, IncludeUnknownLiquidity: true`); `internal/screener/spreads.go:107,293-307` (`maxSpreadsLimit = 500`, sorted by `SpreadBpsNet` desc, then truncated); no age filter in `ComputeSpreads` (ages only reported, `:336-337`); the book never evicts (`book.go` overwrite-only). Lanes not returned are closed as `"lane_gone"` (`alerts/evaluator.go:188-213`). A pair on 14 venues alone yields 182 ordered lanes; the soak book held 7 015 pairs.
- Impact: genuine lanes ranked below 500 are never evaluated, alerted or executed; the slots go to the largest `net_bps`, i.e. mismatches and fresh-vs-stale phantoms that are then refused but still occupy the slots; spurious `lane_gone` closes and "ended" notifications.
- Action: `IncludeSuspect = false` in `spotSignals` (keep counts for diagnostics); filter both legs by age ≤ poll before ranking; lift the cap on the evaluator path (it is a page size, not an evaluation bound).
- Validation: 600 lanes with the only active lane ranked 501st must still open.

### X3 · P1 · The data-age gate plus unsynchronised poll/tick phases resets lifetimes and closes events for non-market reasons; the close reason is not stored
- Evidence: `alerts/evaluator.go:227-236` resets `firstSeen` and closes on any inactive tick including `DATA_AGE`; gate `signals.go:147-159` (each leg ≤ poll, skew ≤ poll/2); poller cadence is `interval + poll duration` (`venue/poller.go:186-200`), the book is written after `Perps()` completes (`:203-220`) though `At` is stamped at spot receipt; the automation tick runs on its own timer (`service_automation.go:185-198`). Soak latencies at a 5 s interval: HTX avg 4 145 ms, BitMart 5 790, Coinbase 7 701, Crypto.com 10 243. `CloseEvent` persists only `closed_at, lifetime_s, peak_net_bps` (`storage/screener_paper.go:20-23`).
- Impact: quote age at a tick ranges over `[D_perps, D_poll + 5 s]`; for HTX (cadence ≈ 9.1 s) the ≤ 5 s leg passes about 3 s in 9, so a 30 s `min_lifetime_s` can never accumulate; even Binance-class venues produce multi-tick fail runs roughly every 10 ticks. Evidence is biased toward fast, phase-aligned pairs; lifetimes understated; each flap sends a notification and starts a cooldown.
- Action: do not reset `firstSeen` on `DATA_AGE` (hold for up to N ticks); persist a close reason; align the tick to run just after the slowest enabled venue's poll or gate on "quote newer than the previous tick".
- Validation: replay with poller cadence 5.5 s, tick 5.0 s, constant active spread → zero closes over 200 ticks.

### X4 · P2 · Funding-history attribution shifted by one interval on venues whose bulk field is the previous period's settled rate
- Evidence: `venue/poller.go:250-265` records `prev.rate` at `prev.next`, correct only if the bulk rate settles at `next`; `venue/bitmart.go:43-48,312` (`rate_value`, "previous period"), `venue/cryptocom.go:25-27,264` (`funding_hist`, past settlement), `venue/bitfinex.go:44-51,366` (ambiguous). Consumers: `perp.go:522-547`, `perp.go:299`, `signals.go:488-527`.
- Impact: display only today (BitMart perps carry no bid/ask; Crypto.com perps are USD-quoted and excluded), but any future execution there would book each settlement with the prior period's rate.
- Action: per-collector rate-semantics flag; store `(T_k, r_k)` after the advance for previous-period venues, or fetch the per-symbol history at settlement.

### X5 · P2 · Restart loses evaluator state: open events stay open forever, cooldowns reset, pending slippage measurements are dropped
- Evidence: `alerts/evaluator.go:68-73,91-104` (in-memory lanes/states, no rehydration); `paperexec/executor.go:64,362-383` (in-memory pending slip); `summary.go:158-167` and `report/stats.go:283-290` average lifetimes over closed events only while `Alerts` counts all rows.
- Action: on start, close `closed_at IS NULL` events as `restart` (or rehydrate); persist `lastOpen` per lane and pending slip legs.

### X6 · P2 · Reports omit open positions: no unrealised mark, accrued funding or open count; `net_pnl_quote` is not labelled realised
- Evidence: `report/stats.go:179-191` samples closed positions only; `markdown.go:43-90` has no open-position row; the API view carries side-correct marks (`paperexec/summary.go:70-91`).
- Impact: with 30-day carry holds the nightly report reads `n=0, net 0` for weeks while positions carry exposure.
- Action: add `open_positions`, `unrealised_mark_quote` (bid to exit spot, ask to exit perp, with age), `funding_accrued_open`; rename the row `realised_net_pnl_quote`.

### X7 · P2 · The book never evicts: delisted pairs and offline venues persist in the table, the guard's median, `pairs_tracked` and the lane cap
- Evidence: `book.go:46-64` overwrite-only; `spreads.go:190-276` no age filter; `guard.go:91-96` median over every peer; collectors stop emitting non-tradable instruments but the last quote persists.
- Action: evict quotes/perps older than K × poll (and on delist); `max_age_ms` on the spreads route defaulting to 3 × poll.

### X8 · P2 · Perp legs have no depth: published sizes are discarded, so the executor fills the perp leg at any size
- Evidence: `types.go:115-137` (`Perp` has no size fields); `binance.go:244-247`, `bybit.go:204-207`, `kucoin.go:290-293`, `bingx.go:277`, `htx.go:363-367` drop `bidQty/askQty`; `alerts/signals.go:345-349` liquidity = spot ask notional only; `paperexec/perp.go:87-91` haircut on the spot quantity only.
- Action: add `BidQty/AskQty` to `Perp`, apply the §1.3 haircut to both legs, report `liquidity_unknown` where absent.

### X9 · P2 · Hot-path cost scales with ledger size; the executor sleeps real latency while holding its mutex inside the single automation goroutine
- Evidence: `paperexec/spot.go:201-203` → `ListExecutions(ctx, ruleID, 0)` (`storage/screener_paper.go:219-221`: limit 0 → 20 000 rows with JSONB) per opened event; `summary.go:28,106-110,158` re-reads the ledger per HTTP request; `executor.go:160-161` holds `x.mu` through `simulateLeg`; `app/components.go:219` passes no `Waiter`, so `RealWaiter` sleeps 100–260 ms per execution inside the tick.
- Action: incremental drift per (rule, lane); cache the auto-paper view per tick; virtual waiter for paper (latency is a model input, not wall-clock).

### X10 · P2 · Coinbase: one 429 discards the whole poll and the collector never adapts its burst
- Evidence: `venue/coinbase.go:159-164` returns `nil, err` on the first failing `product_book` although earlier books are already known; 40 books per poll on an 8 req/s gate (`coinbase.go:79`); `ratelimit.go:176-190` blocks 60 s without Retry-After; `settings.go:216-223` disables Coinbase by default. The classifier itself is correct.
- Action: publish the partial set on 429 (degraded poll); halve `BooksPerPoll` per 429 and recover slowly.

### X11 · P2 · Calculator read model has no data age and bypasses the identity/liquidity guard
- Evidence: `calculator.go:25-37,44-92,90`; `api/screenerapi.go:406`.
- Action: add `buy_age_ms`, `sell_age_ms`, `suspect`, `liquidity_unknown`; refuse suspect lanes.

### X12 · P3 · Reservation errors discarded; a fill exceeding its hold leaves a stuck reservation and silent balance drift
- Evidence: `paperexec/spot.go:71` vs `fill.go:102-107`; `perp.go:119` vs a favourable re-read; `spot.go:106,112`, `perp.go:148,201` `_ = w.Settle(...)`; `CheckInvariants` never called in paperexec. Booked PnL unaffected (computed from fills).
- Action: reserve with the slip factor; check `Settle` errors; run `CheckInvariants` after each execution.

### X13 · P3 · Funding-accrual edge cases and a side mismatch in the basis stop
- Evidence: `perp.go:284-292,306` (retry counter reset only on the found path); `:282-335` (interval switch skips intermediate settlements); `:294-297` (funding mark not age-gated); `:361,367` (exit-side basis compared with entry-side threshold).

### X14 · P3 · Asset identity rests on three aliases plus a price heuristic; venue instrument limits are collected but never used
- Evidence: `kucoin.go:144-149`, `bitfinex.go:185-190`, `htx.go:203`, `bingx.go:152`, `guard.go:68-117`; `Instrument.TickSize/StepSize/MinNotional` referenced nowhere outside `venue/`; the executor truncates to the rule's global `step_size` default (`rule_params.go:161-164`).

### X15 · P3 · Dead code, duplicated math with divergent semantics, stale text
- Evidence: `executor.go:65,201` `lastTick` never read; `signals.go:97,337` duplicate age field; `var _ = url.Values{}` in three collectors; `ageOK` ≡ `dataAgeOK`; mid ×4; exit-basis formula ×4; annualisation ×2; fee lookup ×4 where `report/generator.go:178-184` ignores `Enabled` while API, alerts and executor honour it; `settings.go:125,128` say `0..100` while `maxFeeBps = 200`; `screenerapi.go:27-34` names 6 of 15 venues; `:140` `"spreads_per_sec": 0` hard-coded.

## Verified correct

- Sides and fees: buy at A's ask, sell at B's bid, both taker fees (`spreads.go:312-320`, `paperexec/spot.go:92-101`, re-read `executor.go:341-360`, PnL `spot.go:143` = design §2.2); last and mid appear in no money path; goldens 12.97 / 2.97 / 25.9 bps (`paperexec/golden_test.go:159-167`) and 9.97/0.97, 29.95/20.95 bps (`alerts/evaluator_test.go:68-104`); transfer explicitly not modelled and stated on the wire (`screenerapi.go:253`); inventory pre-positioned per venue with reservations, depth haircut, partial-fill gate, drift cap.
- Freshness: per-leg age ≤ poll and skew ≤ poll/2 (`signals.go:147-159`), re-checked at execution (`spot.go:22-24`, `perp.go:51-58`), exits paused when stale (`perp.go:347-350`); stale quotes cannot be filled.
- Perps: short receives when F > 0 (`perp.go:298-299`, pinned); interval-aware annualisation `F × 24/h × 365` (0.1095 pinned); interval sources per venue; `funding_unconfirmed`/`funding_not_consistent` gates, breakeven vs hold, harvest cap (43/9 pinned); entry perp bid vs spot ask, exit perp ask vs spot bid; converged exit waits `max(BreakevenN, 1)` settlements; funding accrued from settled history only (`TestFundingAccrualSettledOnlyAndRetry`); carry golden 55.684.
- Execution: limit-IOC tolerance + slip model (`fill.go:70-111`), per-venue quote fees, unwind on a rejected second leg, one open per (rule, base), positions and balances reloaded from the ledger, side-correct marks, no look-ahead.
- Rate gate: 429/418/403 classified with Retry-After; MEXC in-band 510 and HTX "request limit" both classified and tested (`mexc_test.go`, `htx_ratelimit_test.go`); ceilings below documented limits; delisted instruments filtered at every collector; quote assets never merged.
- Reports: no "profit" wording; n, days, weekend days, Wilson CI, mean/median shown; the production-gate checklist never softens a missing measurement.
- Rules: per-lane cooldown, dedup by open event, notifier cooldown; entitlements enforced at API, settings, open and execution (`TestRuleVenueTierEnforced`, `TestEvaluatorEntitlementSkipsAndChannel`).
- Concurrency: `Book` RWMutex with copy-on-read; one goroutine per venue with cancel/WaitGroup and self-heal (`poller_restart_test.go`); single automation goroutine with panic recovery; DB writes per poll only on a settlement advance.
- No `float64` in any screener package; unauthenticated GETs only; auto-paper writes only `screener_paper_*`; RBAC/CSRF/audit/`parent_version` on every mutation.
- Tests pinned to hand-computed values: spot/carry/harvest goldens, worked-example signals, breakeven 43/9, `TestComputeSpotKnownAnswers`, `TestWilsonKnownValues`, `TestComputeSpreadsGoldenMath`, `TestCalculateGoldenMath`, guard verdict order, funding sign, stop ordering. No test covers X1–X5.
