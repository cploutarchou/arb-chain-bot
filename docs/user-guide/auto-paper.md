# Auto-paper

Console page **Scanner Suite → Auto-Paper**; API
`GET /api/v1/screener/auto-paper` (`screener:view`). Auto-paper is the
automatic **simulated** execution of alert signals. It exists to turn
alerts into evidence — how many were executable, at what size, what they
returned after modelled costs — not to trade. Live execution stays
disabled in code regardless of any rule's `auto_paper` flag.

The page header states the model in one line: paper only, through the
existing paper ledger, inventory held on both venues, no rebalancing
simulated, funding accrued per interval for carry positions.

## What is simulated

| Aspect | Model |
|---|---|
| Fills | taker, against the **top of book only**: `fillable = min(size, depth_haircut × top_qty)`; smaller than requested is a skip (`DEPTH`) unless the rule allows partial fills |
| Slippage | a per-rule allowance (default 2 bps per leg) is applied to the fill price; **realised** slippage is measured on the next poll and reported next to the allowance |
| Latency and rejection | the simulation package's latency draw and limit-IOC semantics: if the re-read price is worse than the 20 bps tolerance the leg is **rejected**, not filled |
| Fees | taker on every leg, from the venue fee table; token discounts off |
| Cross-venue spot | both legs in one execution with pre-positioned inventory; PnL = `size × (bid_fill × (1 − f_sell) − ask_fill × (1 + f_buy))`; inventory drift per lane is tracked and a conservative rebalance charge and FIFO matched-pair PnL are reported |
| Carry (spot long + perp short, one venue) | both legs at 1× notional, full collateral posted; funding booked at every settlement from the **settled** rate in `funding_history` (never predicted), one ledger row per settlement; closes on convergence, funding exit, max hold or a stop |
| Funding strategy (`funding_harvest`) | same legs, funding-driven entry on the positive-funding side only; the negative mirror needs a margin borrow that is not modelled and is not executed |
| Stops for perp legs | a **hard stop** when mark moves against the short by `margin_stop_frac` (default 50 %) — the position shows as `stopped (maintenance margin)`; basis blow-out stop; funding reversal stop; a stale leg pauses exits and never enables entries |
| Capital | at most `paper_size_quote` per execution, never leveraged; one open position per (rule, base); balances reserved through the reservation service per venue |

## What is NOT simulated

- **Transfers between venues.** Inventory is assumed on both sides. The
  drift row shows what that hides; the rebalance charge is a conservative
  estimate (`drift_notional × (f_A + f_B)`), not a simulated transfer.
- **Depth beyond the top level.** Nothing walks the book.
- **Withdrawal or deposit availability.** Network status is `unknown` on
  every venue that gates it behind an API key; the executor does not check
  it. A spread on an asset you cannot move may be untradeable.
- **Leverage, liquidation mechanics, interest on collateral.** Perps are
  modelled at 1× with the hard stop above; that is a simulation choice, not
  how a real account behaves. Entry is skipped when the maintenance margin
  rate is unknown rather than guessed.
- **Instrument minimums.** `min_notional_quote` is 0 unless a rule sets
  it; instrument rules are not fetched by the screener.
- **Your own market impact, venue outages, halted pairs, account
  freezes.** None of it.

## Preconditions and skip reasons

When an event opens on a rule with auto-paper on, the executor checks, in
order, and records the first failure as a `SKIPPED` position with a
reason:

| Reason | Meaning |
|---|---|
| `entitlement` | the package does not include this strategy, or the open-position cap is reached; the rule still alerts |
| `DATA_AGE` | a leg older than one poll interval, or legs more than half an interval apart |
| `SETTLEMENT_NEAR` | (perp kinds) next funding settlement closer than 2× poll interval |
| `SUSPECT_MISMATCH`, `LIQUIDITY_UNKNOWN` | the shared guard (see [Screener](screener.md)) |
| `MMR_UNKNOWN` | (perp kinds) `params.mmr` not set on the rule |
| `OPEN_POSITION` | one open position per (rule, base) |
| `BALANCE` | venue or perps disabled in settings, or simulated balance short on either leg |
| `DEPTH` | top-of-book size does not cover the request, or quantity truncates to zero |
| `MIN_NOTIONAL` | below the rule's minimum notional |
| `DRIFT_CAP` | (spot) inventory drift in that direction beyond `max_drift_quote` (default 3× paper size) |
| `REJECTED` | a leg failed the limit-IOC tolerance re-read |
| `UNWIND` | (perp kinds) the second leg rejected; the first was unwound at once and its cost booked |
| `partial_leg` | (spot) one leg rejected after the other filled; booked as a one-legged execution marked to market — the strategy's real asymmetric loss |

## Reading the per-rule evidence table

One row per rule that has auto-paper on or has ever produced a position.
Every number is the backend's string; the console only picks which skip
reason is largest.

| Column | Meaning |
|---|---|
| Rule / Strategy | rule id and its effective strategy (`cross_venue_spot`, `carry`, `funding_harvest`) |
| Alerts fired | events opened for the rule (all time, subject to history depth) |
| Executed | positions opened (closed plus still open) |
| Skipped (top reason) | the largest skip bucket; expand the row for the full breakdown |
| Net PnL (quote) | sum of closed-position PnL **plus** unwind and partial-leg costs, in quote currency; `0.00` in neutral colour when n = 0 |
| Hit rate | share of closed positions with PnL > 0; `thin` badge when fewer than 30 samples |
| Mean lifetime (s) | mean alert lifetime (spot) or holding time (perp kinds) |
| Sample size (n) | always its own column, shown even at 0 — the hit rate is never allowed to stand without it |

The API view also carries per-rule matched pairs and matched-pair net,
PnL after rebalance, fees, funding rows and funding net, realised slippage
mean and p95 against the allowance, inventory drift rows (base, venues,
drift, notional, unmatched count, mark age), open-position count and
oldest open age.

**Open positions** table: id, rule, strategy, pair, buy → sell venues,
status (`open`, `stopped (maintenance margin)`, or closed with the reason
— `converged`, `funding_exit`, `max_hold`, `margin_stop`, `basis_stop`,
`funding_reversal`, `interval_switch`, `post_breakeven_nonpositive`),
opened time, and net PnL in quote (unrealised mark for open positions,
with its own data age on the wire).

Executions also appear, tagged by strategy, in Paper Trading and PnL &
Analytics, and every run is audited.

## Production gate — summary

Auto-paper evidence feeds the production gate of
`docs/design/strategy-models.md` §8. Per strategy and rule set, **all** of
the following, from at least 30 consecutive days of unattended auto-paper
filed under `docs/campaigns/<strategy>/`:

1. Duration and coverage — ≥ 30 days; ≥ 20 calm, ≥ 5 volatile, ≥ 4 weekend
   days.
2. Minimum sample — spot: ≥ 200 executions and ≥ 60 matched pairs, ≥ 30 per
   regime; perp strategies: ≥ 30 closed positions, ≥ 8 per regime, ≥ 90
   booked funding settlements.
3. Positive net in every regime using the conservative figure
   (matched-pair / after-rebalance for spot; including unwind costs).
4. One-sided Wilcoxon on per-sample net bps (α = 0.05) and a daily-block
   bootstrap 95 % CI with lower bound > 0 — both, overall and per regime.
5. The stress grid (fees +5 bps per leg, fills 50 %) stays net-positive.
6. Max drawdown ≤ 5 % of allocated paper capital; concentration ≤ 40 %;
   no single-leg or unwind loss larger than 10× the median winning sample.
7. Model honesty — realised slippage p95 within the allowance; no
   unverified fee in the executed venue set.
8. Non-statistical items — security review, decision record, human-merged
   removal of `ErrLiveTradingDisabled`.

Failing any item means live stays disabled. Passing is a necessary
condition for a separate product and legal decision, not a promise of live
performance, and not an upgrade path from any package. The nightly
[reports](reports.md) evaluate what the ledger can evidence and mark the
rest "no evidence yet". As of 2026-08-27 no strategy has evidence.

---

> **Risk summary.** {{brand}} measures and simulates; it does not trade,
> hold funds, hold your exchange keys or advise. Spreads, carry and
> simulated results are measurements net of modelled fees, not predictions.
> Many measured spreads cannot be traded: quotes move, depth is thin,
> transfers are slow or blocked, withdrawal status is unknown, venues fail.
> Simulations exclude transfers, assume top-of-book fills, model
> perpetuals at one-times notional with a hard stop, and use taker fees.
> Simulated results are prepared with hindsight and no account has traded
> them. Nothing on this page is a promise of any outcome. Read the full
> [Risk Disclosure](/legal/risk-disclosure).
