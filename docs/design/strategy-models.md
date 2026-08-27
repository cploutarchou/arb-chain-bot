# Strategy models — entry/exit, cost, risk, paper execution, statistics, production gate

Status: DESIGNED 2026-08-27 (Phase 24, T-077..T-080; consumed by T-071 auto-paper). Owner: derivatives quant. Scope: the five strategies of `docs/design/crypto-arb-platform-command.md` §3. Every number here is a model input or a worked example, never a result; results live only in `docs/campaigns/`. Nothing in this document is a guarantee of profit.

Inputs this design builds on: `docs/design/scanner-suite.md` §3–§4, `docs/research/screener-endpoints.md` (fields and fee verification status), `docs/research/fees.md`, `docs/research/triangular-constraints.md`, `internal/simulation` (latency/limit-IOC fill model), `internal/fees` (fee placement), `docs/risk.md`.

## 0. Conventions

- **Decimal only.** Every price, quantity, rate, fee, funding amount and PnL is `shopspring/decimal` (Go) / string-decimal on the wire. `float64` never touches a money path; annualised carry and percentages are computed in decimal and rounded only at display.
- **Units.** Prices in quote per base; quantities in base; notional in quote; rates as fractions (0.001 = 10 bps); bps = rate × 10 000; time in seconds (UTC epoch ms on the wire). Funding rates are per-interval fractions exactly as the venue publishes them; the annualised figure is display only.
- **bps conversions.** `x_bps = x_frac × 10_000`. Compose multiplicative costs exactly (`(1−f₁)(1−f₂)`), never by adding bps, except where the text says "≈".
- **Rounding.** Quantities are truncated (never rounded) to the venue step size before pricing; fees are computed and stored at full precision.
- **Executable sides.** Buy at ask, sell at bid, always. Mid prices appear only in the basis display formula and never in a PnL.
- **Fee status.** Binance spot taker 0.100 % and USDⓈ-M taker 0.050 % are VERIFIED (screener-endpoints §1.6). OKX, Bybit, Bitget, Gate, MEXC fees are UNVERIFIED. Worked examples use Binance; when a second venue is needed the example says "second venue fee assumed 10 bps (UNVERIFIED)".
- **Contract specs used.** Binance funding: `F = [avg premium index + clamp(interest − premium, ±0.05 %)] / (8/N)`, interest 0.01 % per 8 h, settled 00/08/16 UTC by default, cap/floor = ±0.75 × maintenance margin rate (BTCUSDT ±0.3 %, most others ±2 %); interval switches to 1 h when the rate hits its cap in extreme volatility and reverts to 4 h after 16 consecutive intervals with |F| ≤ 0.025 %; funding is owed only if a position is open at the settlement timestamp (Binance FAQ 360033525031, read 2026-08-27). Binance BTCUSDT tier-1 maintenance margin rate 0.40 %: DERIVED from the published cap (0.3 % = 0.75 × MMR) — treat as UNVERIFIED-DIRECT until read from the rendered bracket table. Bybit BTCUSDT MMR 0.5 % for position value ≤ 2 000 000 USDT (help centre, indexed snippet, 2026-08-27).

## 1. Shared model components

### 1.1 Quotes and data age

Per venue, per symbol: `Quote{bid, bidQty, ask, askQty, ts_recv}`; `Perp{mark, index, bid, ask, bidQty, askQty, funding, predictedFunding, nextFundingAt, intervalH, ts_recv}`.

`age_ms = now − ts_recv`. **Data-age gate (all strategies):** every leg's `age_ms ≤ poll_interval_ms` at decision time, and `|age_A − age_B| ≤ poll_interval_ms / 2` across legs (a fresh leg against a stale leg manufactures phantom spreads). Funding fields: `age ≤ 2 × poll_interval` and `nextFundingAt > now`. Any gate failure → skip with reason `DATA_AGE`, never a trade.

### 1.2 Fees

Taker only on every leg. Spot fees follow the venue's placement (`fees.PlacementFor`); the quote-currency expression `sell_bid × (1 − f_sell) − buy_ask × (1 + f_buy)` is value-equivalent at the fill price and is what the paper ledger books. Perp fees are charged in the settlement currency on notional: `fee = notional × f_perp`. Token discounts are OFF in every model until the pay-asset ledger exists.

### 1.3 Top-of-book fill and slippage model

```
fillable_base = min(size_base, h × topQty)          h = depth haircut, default 1.0, stress 0.5
if fillable_base < size_base and rule.partial_allowed == false → skip (reason DEPTH)
fill_price_buy  = ask × (1 + slip_frac)             slip_frac = slip_bps / 10_000
fill_price_sell = bid × (1 − slip_frac)
```

`slip_bps` is a per-rule allowance: default 2 bps for majors, 10 bps outside the top-20 by liquidity (operator-set, versioned in screener settings). Latency is drawn by the simulation package's model (`SubmitBase 20 ms + U(0,30 ms)`, `FillBase 30 ms + U(0,50 ms)`, seeded per cycle). **Realised slippage** is measured after the fact as `(next_poll_price − quote_price)/quote_price × 10_000` on the traded side and reported next to the allowance. Limit-IOC semantics from `internal/simulation` apply with `LimitToleranceBps = 20`: if the re-read price is worse than tolerance the leg is REJECTED, not filled.

### 1.4 Funding accrual

At each settlement timestamp `T_i` of a venue, for every open perp position:

```
funding_i = notional_i × F_i                notional_i = |qty| × mark(T_i)
short receives funding_i when F_i > 0, pays when F_i < 0; long the reverse
```

`F_i` is the **settled** rate fetched from funding history after `T_i`, never the predicted rate. One `funding` ledger row per settlement `(venue, symbol, T_i, F_i, mark, amount_quote)`. Interval `h` is re-read every poll (Binance `fundingInfo` else 8 h; Bybit `fundingInterval`; Gate `funding_interval`; OKX/MEXC/Bitget per instrument). Predicted funding is used only for entry/exit decisions and always labelled "predicted".

Annualised carry (display): `carry_apr = F × (24 / h) × 365`. `F = 0.0001` per 8 h → 10.95 % APR = Binance's neutral rate, i.e. zero premium over neutral.

### 1.5 Transfer variant (only if a rule opts in; default off)

```
transfer_cost_quote = withdraw_fee_asset × mark + notional × σ_ann × sqrt(t_transit / 31_536_000)
```

Example: 0.2 BTC at 50 000, 30-minute transit, σ_ann 0.50 → `10_000 × 0.5 × 0.00756 = 37.8 USDT` ≈ 38 bps before the withdrawal fee. Withdrawal fees per network are UNVERIFIED for every venue and must be entered by the operator in the calculator.

### 1.6 Capital, 1× notional, reservation

At most `paper_size_quote` per execution, never leveraged: perp collateral posted = full notional. Reservations go through `internal/reservation` with a per-venue paper balance. One open position per (rule, base) at a time.

## 2. Cross-venue spot (inventory on both venues, no transfers)

### 2.1 Entry

```
gross_bps = (bid_B − ask_A) / ask_A × 10_000
net_bps   = (bid_B × (1 − f_B) − ask_A × (1 + f_A)) / ask_A × 10_000
exec_bps  = net_bps − slip_bps_A − slip_bps_B − buffer_bps         (buffer default 5)
```

Enter when `exec_bps ≥ rule.min_spread_bps` continuously for `≥ rule.min_lifetime_s`, `liquidity_quote = min(askQty_A × ask_A, bidQty_B × bid_B) ≥ rule.min_liquidity_quote`, data-age gate passes, and paper balances hold ≥ `size_quote` on A and ≥ `size_base` on B.

Size: `size_base = trunc_step(min(size_quote / ask_A, h × askQty_A, h × bidQty_B))`; skip below either venue's min notional.

### 2.2 Exit

Instantaneous: both legs filled in the same execution. `pnl_quote = size_base × [bid_B^fill × (1 − f_B) − ask_A^fill × (1 + f_A)]`.

### 2.3 Inventory drift and rebalancing charge

```
drift_base(t)  = Σ base bought on A − Σ base sold on B (signed, per venue)
drift_notional = |drift_base| × mid_ref
```

Reset costs: reverse trade `rebalance_bps = f_A + f_B + max(0, −net_bps_at_reversal)`; or transfer (§1.5). Report `pnl_after_rebalance = Σ pnl_quote − drift_notional × (f_A + f_B)` (conservative) and the **matched-pair PnL** (FIFO A→B matched with later B→A executions of the same base) — the statistic the gate uses. Unmatched drift is shown as open exposure marked at mid.

### 2.4 Risk

No leverage, no open position. Stops: (i) `exec_bps` falls below threshold mid-lifetime → alert closes; (ii) one leg REJECTED by the tolerance re-read → the other leg is still executed and booked as a one-legged execution with its mark-to-market, counted in `skipped.partial_leg` — the strategy's real asymmetric loss; (iii) `rule.max_drift_quote` (default 3 × `paper_size_quote`) → further executions in that direction skipped with `DRIFT_CAP`.

### 2.5 Worked example (Binance A; venue B fee 10 bps UNVERIFIED)

BTC/USDT: `ask_A = 50 000.00`, `askQty_A = 0.35`; `bid_B = 50 150.00`, `bidQty_B = 0.20`; `size_quote = 5 000`.
- `gross_bps = 30.00`; `net_bps = (50 099.85 − 50 050.00)/50 000 × 10 000 = 9.97`; `exec_bps = 9.97 − 2 − 2 − 5 = 0.97` → below `min_spread_bps` 5: **skip**. A 30 bps gross spread on a 10/10 pair is not tradeable at retail taker rates.
- At `bid_B = 50 250` (50 bps gross): `net_bps = 29.95`, `exec_bps = 20.95` → trade. `size_base = 0.10000`. Fills: buy 50 010.00, sell 50 239.95. `pnl_quote = 0.1 × (50 189.71 − 50 060.01) = 12.97 USDT` (25.9 bps). Rebalance charge at 20 bps of the 5 001 drift notional = 10.00 → `pnl_after_rebalance = 2.97 USDT`.

### 2.6 Paper execution algorithm (each poll)

1. Refresh quotes; compute ages.
2. For each `spread` rule and (base, quote, A, B) in its filters: compute `net_bps`, `exec_bps`, liquidity; update lifetime tracker.
3. Lifetime ≥ `min_lifetime_s` and no cooldown → open `screener_event`; Telegram if enabled.
4. If `auto_paper`: preconditions (age, liquidity, balances, drift cap, min notional); failure → `skipped[reason]++`.
5. Reserve `size_quote` on A and `size_base` on B (idempotency key = event id).
6. Simulate leg A (buy) and leg B (sell) per §1.3.
7. Book a paper cycle tagged `cross_venue_spot` with both fills, fees, `pnl_quote`, realised slippage; update balances and drift; release reservation.
8. Update per-rule summary.

## 3. Spot + perp carry (same venue)

Long spot, short USDT-M perp, both 1× → delta-neutral.

### 3.1 Entry

```
basis_entry_bps = (perp_bid − spot_ask) / spot_ask × 10_000
fees_rt_bps     = 2 × f_spot_bps + 2 × f_perp_bps
funding_exp_bps = Σ_{i=1..n_hold} F̂_i × 10_000   (F̂₁ predicted, then mean settled of last 30 intervals)
edge_bps        = basis_entry_bps + funding_exp_bps − fees_rt_bps − 2 × slip_bps − buffer_bps
```

Enter when `edge_bps ≥ rule.min_edge_bps` (default 10), `F̂₁ > 0`, data-age gate, liquidity both legs, balances on spot and futures wallets. `n_hold = rule.max_hold_h / h`. `qty = trunc_step_both(size_quote / spot_ask)`.

### 3.2 Exit

Close both legs (sell spot at bid, buy perp at ask) on the first of: convergence `basis_now_bps ≤ rule.close_bps` (default 0); predicted funding ≤ `rule.exit_funding` for `k` consecutive intervals (default 2); max hold; a §3.4 stop.

```
pnl_quote = qty × [spot_bid^close × (1 − f_spot) − spot_ask^open × (1 + f_spot)]
          + qty × (perp_bid^open − perp_ask^close) − f_perp × qty × (perp_bid^open + perp_ask^close)
          + Σ funding_i
```

### 3.3 Cost model

Binance: `fees_rt_bps = 30`; slippage 2 bps × 4 legs = 8. Funding only from settled rates. Perp collateral = `qty × perp_bid^open` (no interest modelled).

### 3.4 Risk

- **Margin stop.** Short perp, `m = 1`: `P_liq = P_open × (1 + m)/(1 + MMR)`; BTCUSDT MMR 0.004 → `1.992 × P_open`. Hard stop: close both legs when `(mark − P_open)/P_open ≥ rule.margin_stop_frac` (default 0.50); skip entry when MMR unknown (`MMR_UNKNOWN`).
- **Basis blow-out stop.** Close when `basis_now_bps − basis_entry_bps ≥ rule.basis_stop_bps` (default 100 BTC/ETH, 300 otherwise).
- **Funding reversal.** Close when `Σ funding_i < −(basis_entry_bps/10_000 × notional)`.
- **Data-age gate** every poll; a stale leg pauses exits, never enables entries.

### 3.5 Worked example (Binance, verified fees)

Spot ask 50 000 / bid 49 998; perp bid 50 100; predicted funding +0.0100 %/8 h, last-30 mean +0.0120 %; size 10 000; max hold 30 d (90 intervals).
- `basis_entry_bps = 20.00`; `funding_exp_bps = 1.00 + 89 × 1.20 = 107.80` (predicted); `edge_bps = 20 + 107.8 − 30 − 8 − 5 = 84.8` → enter.
- Fills: spot buy 0.2 at 50 010 (fee 10.002), perp sell 0.2 at 50 089.98 (fee 5.009); collateral 10 018.
- Realised funding averaged +0.008 % over 90 settlements: `Σ funding ≈ 72.00 USDT`.
- Close at spot bid 51 000 / perp ask 51 010 (max-hold exit, basis not converged): spot leg +177.80; perp leg −184.00 − 10.11 = −194.11; total **55.69 USDT** (55.7 bps over 30 days ≈ 6.8 % APR display). All of it came from funding; at +0.003 % mean funding the trade nets ≈ −17 USDT.
- Margin stop at 75 135 (50 %); liquidation would be ≈ 99 779.

### 3.6 Paper execution algorithm (each poll)

1. Refresh spot, perp, funding fields; ages.
2. For every open position: if `now ≥ nextFundingAt_seen`, fetch settled `F_i` (retry ≤ 3 polls) and book funding at `qty × mark(T_i)`; re-read `h`; evaluate exits/stops; on trigger simulate close legs, book close tagged `carry`, release collateral.
3. For every `carry` rule: compute `edge_bps`; lifetime tracker; event/Telegram.
4. Preconditions (age, liquidity, balances, no open (rule, base), MMR known, next settlement ≥ `2 × poll_interval` away).
5. Reserve; simulate spot buy then perp sell (thinner leg first as a rule option); if the second leg REJECTs, unwind the first immediately and book the cost as `skipped.unwind`.
6. Book open position `{qty, spot_open, perp_open, fees_open, collateral, nextFundingAt, h}`.

## 4. Futures–futures (same base, two venues)

Long the cheaper perp on L, short the richer on S.

### 4.1 Entry / exit

```
spread_entry_bps = (perp_bid_S − perp_ask_L) / perp_ask_L × 10_000
fund_diff_bps    = Σ over hold of [F̂_S × 10_000 − F̂_L × 10_000], each on its own venue interval
fees_rt_bps      = 2 × f_perp_L + 2 × f_perp_S
edge_bps         = spread_entry_bps + fund_diff_bps − fees_rt_bps − 4 × slip_bps − buffer_bps
```

Enter when `edge_bps ≥ rule.min_edge_bps`. Exit on `spread_now_bps ≤ close_bps`, funding differential ≤ 0 for `k` intervals, max hold, or stop. PnL = both legs − 4 fees + Σ funding_S − Σ funding_L, each on its venue's settlement clock.

### 4.2 Risk

Two margin accounts, both 1×. Long: cannot be liquidated by price alone. Short: `P_liq,S = P_open × 2/(1 + MMR)`. Stops as §3.4 (`basis_stop_bps` default 50 for BTC/ETH). **Venue divergence**: if a venue's mark and index diverge > `rule.index_div_bps` (default 100) the pair is skipped/closed (`INDEX_DIVERGENCE`).

### 4.3 Worked example (Binance L 5 bps verified; Bybit S 5.5 bps UNVERIFIED)

`perp_ask_L = 50 000`, `perp_bid_S = 50 040` → 8 bps; fund diff 0.2 bps/8 h × 30 = 6; `fees_rt = 21`; slippage 8; buffer 5 → `edge = −20` → skip. At 40 bps spread → `edge = +12` → 0.2 BTC; spread closes to 0 in 10 days: spread pnl 40.00, fees ≈ 21.01, funding ≈ +2.00 → net ≈ **20.99 USDT** on 20 000 gross notional (10.5 bps).

### 4.4 Paper execution — as §3.6 with two perp wallets and two settlement clocks; short leg first when thinner.

## 5. Funding-rate harvest

Same legs as §3, funding-driven entry, positive-funding side only (the negative mirror needs a margin borrow that is not modelled → shown as "requires borrow, not executed").

### 5.1 Entry

```
F̂_bps       = min(predicted_bps, mean(settled last 6)_bps)
breakeven_n = ceil((fees_rt_bps + 4 × slip_bps + buffer_bps) / F̂_bps)
```

Enter when `F̂_bps ≥ rule.min_funding_bps` (default 3/interval), `breakeven_n ≤ rule.max_breakeven_intervals` (default 12), `basis_entry_bps ≥ −rule.max_negative_basis_bps` (default 10), data-age gate, next settlement ≥ `2 × poll_interval` away. Binance: costs 43 bps; at 1 bps/8 h breakeven 43 intervals (14.3 d) → skip; at 5 bps/8 h breakeven 9 (3 d) → enter.

### 5.2 Exit

Predicted funding ≤ `rule.exit_funding_bps` (default 1) for 2 intervals; interval switches to 1 h at cap (squeeze regime); cumulative funding + basis change ≤ 0 after breakeven; §3.4 stops.

### 5.3 Worked example (Binance)

Predicted +0.05 %/8 h, last-6 mean +0.045 %, basis −2 bps, size 10 000 → enter 0.2 BTC. 12 settlements realised mean +0.035 %: `Σ funding ≈ 42.00`; fees 30.00; realised slippage 4.00; basis closed at −6 bps → −4.00 → net ≈ **4.00 USDT** (4 bps) over 4 days. One capped interval at −0.3 % (−30 USDT) flips it negative — the distribution over many positions is the statistic that matters.

## 6. Triangular (existing engine)

Formulas, constraints and the §80 stress grid are in `docs/research/triangular-constraints.md`; paper executor `internal/simulation`. Reference result `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3/` (zero qualified cycles) and T-063 live scanning (best net −36 bps ≈ +4 bps gross against 40 bps of costs). Sample unit = ALL_FILLED cycle; partial/failed cycles are a separate PnL line.

## 7. Statistics (per strategy, per rule, per regime)

Sample unit: cross-venue spot = one execution (and one matched pair); carry / futures-futures / funding = one closed position; triangular = one cycle.

| statistic | definition |
|---|---|
| `n`, `n_regime` | sample counts overall and per calm/volatile/weekend |
| `net_pnl_quote` | Σ pnl after fees, funding, realised slippage; spot also `pnl_after_rebalance` and matched-pair net |
| `net_bps_mean`, `net_bps_median` | per-sample `pnl / notional_deployed × 10_000` |
| `hit_rate` | share of samples with `pnl > 0`, with Wilson 95 % interval |
| `lifetime_s` mean/median | alert lifetime (spot); holding time (others) |
| `max_drawdown_quote/frac` | peak-to-trough of cumulative equity incl. unrealised marks and unmatched inventory, over allocated paper capital |
| `inventory_drift` | per base: `drift_base`, `drift_notional`, unmatched count |
| `skipped{reason}` | DATA_AGE, DEPTH, BALANCE, DRIFT_CAP, MMR_UNKNOWN, UNWIND, INDEX_DIVERGENCE… |
| `funding_rows`, `funding_net_quote` | carry/funding strategies |
| `realised_slip_bps` mean/p95 per leg | allowance calibration |
| `concentration` | largest single day's share of total net PnL |

Regimes from stored quotes: `rv24 = stdev(log returns of BTC/USDT 1-min mids over 24 h) × sqrt(525 600)`; calm `< 0.40`, volatile `≥ 0.60`, in-between counted in neither; weekend = Saturday 00:00 UTC → Monday 00:00 UTC. Thresholds live in settings and are printed in every report.

## 8. Production gate (per strategy, per rule set)

All of the following, from ≥ 30 consecutive calendar days of unattended auto-paper execution, filed under `docs/campaigns/<strategy>/`:

1. **Duration and coverage.** ≥ 30 days; ≥ 20 calm, ≥ 5 volatile, ≥ 4 weekend days (extend, never trim).
2. **Minimum sample.** Spot: `n ≥ 200` executions and `≥ 60` matched pairs, `≥ 30` per regime. Carry / futures-futures / funding: `≥ 30` closed positions, `≥ 8` per regime, `≥ 90` booked funding settlements. Triangular: `≥ 200` ALL_FILLED cycles, `≥ 30` per regime.
3. **Positive net in every regime** using the conservative figure (matched-pair / after-rebalance for spot; including unwind costs; including the failed-cycle line for triangular).
4. **Statistical test.** One-sided Wilcoxon signed-rank on per-sample `net_bps`, `H₀: median ≤ 0`, α = 0.05, overall and per regime; plus a daily-block bootstrap (10 000 resamples) 95 % CI of mean `net_bps` with lower bound > 0. Both must pass. At n = 200 the sign test detects hit rate 0.60 vs 0.50 with power ≈ 0.8 — the minimums are floors, not targets.
5. **Stress.** The §80 grid over the same window with fees +5 bps per leg and fills 50 % (h = 0.5) stays net-positive overall.
6. **Drawdown and concentration.** `max_drawdown_frac ≤ 0.05`; `concentration ≤ 0.40`; no single-leg/unwind loss larger than 10 × the median winning sample.
7. **Model honesty.** `realised_slip_bps p95 ≤` allowance, else raise it and restart the window. No UNVERIFIED fee in the executed venue set (scanned, not counted).
8. **Non-statistical items** from the command still apply (security review, decision record, human-merged removal of `ErrLiveTradingDisabled`).

Failing any item means LIVE stays disabled; the report states which item failed. Passing is a necessary condition, not a promise of live performance.

Sources (read 2026-08-27): Binance funding-rate FAQ 360033525031; Binance leverage & margin table (did not render — MMR derived); Binance leverage & margin FAQ 360033162192; Bybit maintenance-margin (USDT contracts) help article.
