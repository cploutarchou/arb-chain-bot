# Trading-logic audit — triangle enumeration, pricing, fees, constraints

Audited tree: `master` at `abddb55` (2026-09-09). Scope: `internal/graph`,
`internal/pricing`, `internal/fees`, `internal/exchange` (instrument rules,
Binance filter mapping, fee schedule construction), `docs/research/
triangular-constraints.md`, `docs/research/fees.md`. Every worked example
below was hand-computed first and then confirmed by executing the production
code from an isolated scratch module; the repository was not modified.
`go test -race ./internal/graph/... ./internal/pricing/... ./internal/fees/...
./internal/exchange/...` passes.

Verdict on the arithmetic: the core is correct. Orientation, fee placement
for all three venue conventions, quantization direction and order, depth
chaining and dust accounting all reproduce the hand-computed results to the
last digit. The defects are in the size search, in venue-constraint coverage,
in where fee rates come from, and in metadata refresh.

## Worked examples

### Example 1 — plain profitable cycle (Binance, fee in the received asset)

Books, taker 10 bps both sides, start 20 000 USDT:

| Market | Side | Levels | step / tick / minQty / minNotional |
|---|---|---|---|
| BTCUSDT | BUY → asks | 50 000.00 × 0.4; 50 010.00 × 2.0 | 0.00001 / 0.01 / 0.00001 / 5 |
| ETHBTC | BUY → asks | 0.050000 × 10; 0.050020 × 50 | 0.0001 / 0.000001 / 0.0001 / 0.0001 |
| ETHUSDT | SELL → bids | 2 510.00 × 20; 2 509.00 × 100 | 0.0001 / 0.01 / 0.0001 / 5 |

Raw top-of-book cross rate: `2510 / (50000 × 0.05) = 1.0040` → +40.00 bps raw.

- Leg 1 (USDT→BTC, BUY BTCUSDT): budget walk takes level 1 exactly (`50 000 × 0.4 = 20 000`), raw qty 0.4, quantized 0.4, exact cost 20 000, VWAP 50 000. Gross 0.4 BTC, fee 0.0004 BTC (in BTC, the received asset), net 0.3996 BTC. Dust 0.
- Leg 2 (BTC→ETH, BUY ETHBTC; BTC is the quote of ETHBTC): level 1 cost 0.5 > 0.3996 → partial 7.992 ETH, on grid, cost 0.3996, VWAP 0.05. Gross 7.992 ETH, fee 0.007992 ETH, net 7.984008 ETH.
- Leg 3 (ETH→USDT, SELL ETHUSDT): quantize 7.984008 → 7.9840 (dust 0.000008 ETH stranded); proceeds `2510 × 7.984 = 20 039.84`, fee 20.03984 USDT (in USDT), net 20 019.80016 USDT.
- Cycle: input 20 000, final 20 019.80016, profit +19.80016, return **9.90008 bps**. Reconciliation: `1.0040 × 0.999³ = 1.000991011` (+9.91011 bps) minus the leg-3 truncation (`0.000008/7.984008`) = +9.90008 bps. Exact match.
- `opportunity.Build` with the shipped buffers (5 + 5 bps): buffer 20 USDT, estimated final 19 999.80016, net profit −0.19984, net return −0.09992 bps → rejected against `min_net_edge_bps = 5`. A 40 bps raw deviation is a negative net opportunity: the fee wall behaves as `docs/research/triangular-constraints.md` §3 requires.

### Example 2 — fee-on-input venues

QUOTE-convention buy (same leg-1 book, 20 000 USD, 10 bps): `usable = 20 000 / 1.001 = 19 980.01998…`; partial `usable / 50 000 = 0.39960039…` → quantized 0.39960; exact cost 19 980.00; proportional fee `19 980 × 0.001 = 19.98 USD` charged in USD (the spendable quote); `InputConsumed = 19 999.98`, dust 0.02 USD, gross = net = 0.3996 BTC (no second fee). Conservation `19 999.98 + 0.02 = 20 000` holds, and the RECEIVED venue on the same book yields the same 0.3996 BTC for 20 000 — two structurally different fee models agree to the quantization residue.

SPENT-convention sell (Kraken-shaped, 80 bps, 7.984008 ETH into bids 2510 × 20): `usable = 7.984008 / 1.008 = 7.92064…` → 7.9206; fee `0.0633648 ETH` in base; `InputConsumed = 7.9839648`, dust 0.0000432 ETH; proceeds 19 880.706 with no output fee. No double count on any convention.

### Example 3 — depth-exhausted legs

- Leg 1 exhausts (single ask level 50 000 × 0.4, budget 30 000): qty 0.4, cost 20 000, `DepthExhausted = true`, dust 10 000 USDT; cycle `InputConsumed = 20 000` (not 30 000) and return unchanged at 9.90008 bps — undeployed start-asset dust never dilutes the result; `LiquidityLimited = true`.
- Leg 3 exhausts (bids 2 510 × 5, input 7.984008 ETH): sold 5, proceeds 12 550, dust **2.984008 ETH stranded**, fee 12.55, net 12 537.45; cycle profit −7 462.55 (−3 731 bps). The stranded ETH is valued at zero in `CycleQuote` (pessimistic, so the sizer is never attracted to such a size) and separately marked as `Exposure[ETH]` by the paper engine (`internal/simulation/paper.go:234-237`).

## Findings (ranked)

### T1 · P1 · `SizeSearch` can return a loss-making size while a profitable size exists in range
- Evidence: `internal/pricing/sizer.go:58-105` is a 13-point uniform grid plus 14 ternary iterations that assume unimodality; it never projects leg-2/3 depth boundaries back into leg-1 units, although `sizer.go:11-16` and `resources/triangular-math.md` claim breakpoint awareness. Reproduced: asks `50 000 × 0.02` then `60 000 × 100` (breakpoint at exactly 1 000 USDT), `minIn = 1`, `maxIn = CapacityHint = 6 001 000` → sizer returns `in = 1714, profit = −117.73 (−686.9 bps)`; brute force finds `size = 1000 → +0.99 (+9.90 bps)`. Fourteen iterations shrink the bracket by `(2/3)^14 ≈ 1/292`, an effective resolution of `span/1754`. At the shipped defaults (`MinInput 50`, `MaxTradeSize 1000`) the sizer matched brute force exactly; the failure needs `max_trade_size / min_input ≳ 2500`, and `max_trade_size` has no upper bound (`internal/strategy/params.go:157`).
- Impact: false negatives only (the returned quote is always a truthful `QuoteCycle`), reported as large negative edges on genuinely positive triangles at large size ranges.
- Action: build candidates from actual breakpoints (leg-1 cumulative level boundaries; leg-2/3 boundaries mapped back through VWAPs and fee factors), union with `minIn`/`maxIn`, evaluate all, refine between the two best adjacent candidates; or a log-spaced grid with iterations derived from `log(maxIn/minIn)`.
- Validation: for books with a single breakpoint `B`, `Find` returns within one step of `B` for `maxIn/minIn ∈ {10, 10², 10³, 10⁴, 10⁶}`; property test against a 2 000-point reference grid.

### T2 · P2 · Sizer objective and risk gate disagree, producing systematic false negatives
- Evidence: `sizer.go:55` maximises absolute `GrossProfit`; the gate then applies `min_net_edge_bps` (`internal/risk/engine.go:114-115`) and worst-leg `max_price_impact_bps` (default 30, `engine.go:142-151`). On the probe book size 200 has 39.70 bps impact, size 120 has 0.00; nothing feeds the cap back into sizing.
- Impact: a triangle whose maximum-profit size violates the impact cap is rejected outright (`RISK_PRICE_IMPACT`) even when a smaller size passes every gate.
- Action: pass a feasibility predicate (impact cap, minimum net edge after buffers) into `Find`, or run constrained and unconstrained searches and prefer the constrained optimum.

### T3 · P2 · Paper slippage measured against a post-buffer baseline
- Evidence: `internal/simulation/paper.go:244-247` compares `op.EstimatedFinal` (`Quote.FinalAmount − buffers`) to the actual final; a cycle filled exactly as planned records −10 bps "slippage" at the defaults. `docs/research/triangular-constraints.md:106-107` calls measured slippage the platform's most important self-calibration signal. (Rated P0 in `execution-risk-audit.md` F1 because the partial-fill size mismatch compounds it.)
- Action: baseline `op.Quote.FinalAmount` at the deployed size.

### T4 · P2 · Binance `MARKET_LOT_SIZE` is never parsed; MARKET orders are validated against `LOT_SIZE`
- Evidence: `internal/exchange/binance/metadata.go:94-146` handles only `PRICE_FILTER`, `LOT_SIZE`, `NOTIONAL`/`MIN_NOTIONAL`; `MARKET_LOT_SIZE` appears only in `docs/research/fees.md:66`. `internal/simulation/paper.go:185-187` sets `order.Type = "MARKET"` when configured.
- Impact: in MARKET mode the model can "fill" a size the venue would reject (`MARKET_LOT_SIZE.maxQty` is volume-derived and typically far below `LOT_SIZE.maxQty`), overstating capacity.
- Action: add market-order step/min/max to `InstrumentRules`, parse the filter, select by order type in `ValidateOrder`.

### T5 · P2 · Limit prices are never quantized to the tick grid; the price-quantization API is dead code
- Evidence: `internal/simulation/paper.go:336-345` multiplies a depth-weighted VWAP by `1 ± 20 bps`; `InstrumentRules.QuantizePriceDown/Up` (`internal/exchange/instrument.go:83-89`) have no production callers.
- Impact: every simulated LIMIT_IOC order carries a price the venue's `PRICE_FILTER` would reject; the recorded `LimitPrice` is not submittable and the filter cut point sits off-grid.
- Action: `QuantizePriceUp` for buys, `QuantizePriceDown` for sells in `limitPrice` (rules are already resolved two lines earlier).

### T6 · P2 · `ValidateOrder` is applied to the filled quantity, not the submitted quantity
- Evidence: `internal/pricing/pricing.go:150-157` validates `soldQty` after the walk; executed probe: 10 ETH submitted into 0.5 ETH of bids with `minQty = 1` → "quantity below minimum: 0.5 < 1", so a depth-limited leg hard-fails the cycle instead of returning a partial.
- Impact: conservative (lost opportunities, never overstated profit) but makes thin-book triangles look structurally untradeable and turns valid search points into invalid ones (compounds T1).
- Action: validate `orderQty` (submitted) against min/max/notional; record the achieved quantity as a partial.

### T7 · P2 · Fee rates never come from the venue; a hard-coded 10/10 bps seed is the only source
- Evidence: `internal/platform/settings.go:433-436` seeds `MakerBps/TakerBps = 10`; consumed at `internal/app/engine.go:768-778`; no call to `GET /sapi/v1/asset/tradeFee` or `GET /api/v3/account/commission` exists. `docs/research/fees.md:15-17` says "never hardcode them" and names both endpoints (`:61-63`).
- Impact: per `triangular-constraints.md:81-83` a single wrong fee assumption invalidates every result; promo/zero-fee pairs are supported only through manual overrides, so promo expiry silently produces optimistic results. P1 the moment any account is not VIP0.
- Action: a fee-schedule fetcher populating `Schedule.Default` and `PerMarket` from `account/commission` (its `discount` is a multiplier), refreshed on a timer, with provenance in `Effective.Source`; refuse to start on `"default"` when a live fetch is configured.

### T8 · P2 · Topology and instrument rules are built once per run, never refreshed
- Evidence: `internal/app/engine.go:671,707` — `bootstrapMetadata` (the only `ExchangeInfo` caller, `:1228-1235`) and `graph.Build` sit in the linear body of `Run`.
- Impact: a symbol moving to BREAK/HALT/DELISTED or a `LOT_SIZE`/`NOTIONAL` change is invisible until restart; the staleness gate catches a halted stream, not a filter change.
- Action: refresh `exchangeInfo` on a timer, diff, rebuild topology and rules on material change; count rebuilds.

### T9 · P2 · `MaxSlippageBps` never evaluated (see `execution-risk-audit.md` F12)
- Evidence: declared, overridable, validated, defaulted (50) and displayed; no check in `risk.Evaluate` and no `RISK_SLIPPAGE` reason code.
- Action: add the check with a reason code or delete the field; assert every non-zero `Limits` field produces a `Check`.

### T10 · P2 · Exchange constraints duplicated in the screener product line
- Evidence: `internal/screener/paperexec/fill.go:113-119` `truncStep` (`Div(step).Floor().Mul(step)` on a 28-dp result rather than `QuoRem`); a second Binance filter parser at `internal/screener/venue/binance.go:120-133`; a quote-only fee model at `fill.go:108`.
- Action: depend on `exchange.InstrumentRules` and `fees.Placement`; differential test then delete the local helper.

### T11 · P2 · `DefaultSizeSearch` is ~7× its documented latency budget
- Evidence: `sizer.go:22-24` claims sub-millisecond at 50 levels; measured with the repository's benchmarks on this host: `BenchmarkQuoteCycle50Levels 138 032 ns/op`; `BenchmarkSizeSearch50Levels 7 250 997 ns/op, 3 641 949 B/op, 108 231 allocs/op` (41 evaluations).
- Impact: with 2 workers and per-tick re-pricing of every affected triangle, a 7 ms search is an unbudgeted hot-path constraint and caps how far T1 can be fixed by raising grid density.
- Action: correct the comment; pre-size allocations, hoist constants out of loops (`sizer.go:63,82`); cache the leg-1 budget walk across adjacent candidates; pin ns/op and allocations in CI.

### T12 · P3 · Precision, filter semantics and hygiene
- Min-notional uses `VWAP × qty` (`pricing.go:113-114`, `instrument.go:130`) rather than the exact walk cost; pass `cost`.
- `NOTIONAL.applyMinToMarket` / `applyMaxToMarket` / `avgPriceMins` are not parsed (`metadata.go:44-47`; the fixture at `binance_test.go:217` contains them). For a sell LIMIT the model validates `VWAP × qty` while the venue sees `VWAP × (1 − 20 bps) × qty`, so orders within 20 bps of the floor can pass the model and be rejected by the venue.
- `Usable()` accepts a market with no `NOTIONAL` filter (`instrument.go:50-56`; verified `Usable=true, MinNotional=0, ValidateOrder(1, 0.001)=nil`), contradicting `metadata.go:15` and `triangular-constraints.md:193-195`.
- `DepthExhausted` for buys comes from the budget walk, not the quantized walk (`pricing.go:101,120`).
- `impactBps` reads `Asks[0]`/`Bids[0]` even at zero size; reachable only through a non-`Book` view source.
- `fees.Bps` has no callers; `UsableInput`'s fee return is discarded in production (intentional).
- Token-discount honesty is enforced two packages away (`platform.FeeSettings.validate`); move the refusal into `fees`.
- `UsableInput` can leave `InputConsumed` above `input` by ~1e-28 when deployed cost equals `usable` exactly; harmless (guards absorb it), worth an explicit clamp.

## Verified correct (all confirmed by execution)

- Orientation derives purely from `Market.Base`/`Market.Quote` (`internal/graph/graph.go:87-88`). BTCUSDT/ETHBTC/ETHUSDT from USDT yields exactly two triangles: `BTCUSDT>ETHBTC>ETHUSDT` (BUY/BUY/SELL) and `ETHUSDT>ETHBTC>BTCUSDT` (BUY/SELL/SELL). A hypothetically inverted listing BTCETH flips the middle leg to SELL-on-BTCETH with no code change. `pricing.QuoteLeg` never re-derives orientation.
- No last or mid price on the money path; the only best-price consumers are `portfolio.BookMarker` (bid for base→target, ask for quote→target) and the separate screener product.
- Fee placement across all three conventions and both sides matches the hand computations; `NetOutput` no-ops for input-side fees and `UsableInput` for output-side, so no path applies a fee twice or misses one; `GrossOut = NetOut + Fee` and `InputConsumed + Dust = input` hold on every leg (also pinned by `TestLegConservation`).
- `QuantizeQty` floors with exact integer division (`QuoRem`) or `Truncate`; quantization precedes the exact-quantity walk so `cost` is what the venue would charge; min-qty and min-notional validated after truncation.
- `decimal.DivisionPrecision = 28` set once (`internal/exchange/decimal.go`); every `Div` on the pricing path was traced and none reaches an exchange-facing quantity without `QuantizeQty`; no `float64` on any money path.
- Depth-walk edge cases executed: empty sides → `ErrNoDepth`; budget exactly equal to a level → exhausted with zero dust; zero remaining mid-book contributes nothing (no division by zero); `LevelsConsumed` counts partial levels; `PriceImpactBps` non-negative both sides.
- `CapacityHint` is an upper bound including the input-side fee (6 001 000 → 6 007 001 under QUOTE; 10 000 → 10 080 under SPENT at 80 bps).
- Graph invariants executed: duplicate market ids collapse; distinct markets over one pair enumerate separately; two-cycles, self-steps, repeated markets and cross-exchange legs impossible; rejections counted; canonical key uniquely determines every side; stablecoin variants are distinct assets (USDT/USDC/FDUSD → four triangles with correct sides).
- Single source of economics: `pricing.QuoteLeg`/`QuoteCycle` is the only place a conversion is priced; `opportunity.Build` subtracts buffers, `risk.Evaluate` compares, `simulation.fillLeg` re-enters `pricing.QuoteLeg`; the web client computes nothing.
- BNB discount modelled honestly: operator-immutable table, Bybit MNT marked `AppliesToAPI: false`, `token_discount: true` refused until a pay-asset ledger exists; zero-fee promo pairs only through explicit overrides; default fees must be strictly positive.
