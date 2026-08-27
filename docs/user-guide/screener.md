# Screener

Console page **Scanner Suite → Screener**; API `GET /api/v1/screener/spreads`
(`screener:view`). The table lists cross-venue spot lanes — buy on venue A
at the ask, sell on venue B at the bid — for every pair quoted on at least
two enabled venues, refreshed every poll interval. Every number is a
measurement of public top-of-book quotes at a point in time, net of the
configured taker fees. It is not a prediction and it is not a tradeable
profit; see the risk summary at the end of this page.

## Stats strip

| Stat | Meaning |
|---|---|
| Venues online | enabled venues whose last poll succeeded / enabled venues |
| Pairs tracked | (base, quote) pairs with a quote on at least one venue |
| Spreads / sec | lanes evaluated per second across the book |
| Data age | time since the status snapshot was generated |
| Poll interval | `poll_interval_s` from settings; every page refreshes at this cadence |

## Columns

| Column | Wire field | Meaning |
|---|---|---|
| Pair | `base`/`quote` | Quote assets are exact: USDT, USDC, FDUSD and USD are distinct and never merged |
| Buy venue / ask | `buy_venue`, `buy_ask` | the venue and price you would buy at (its best ask) |
| Sell venue / bid | `sell_venue`, `sell_bid` | the venue and price you would sell at (its best bid) |
| Gross bps | `spread_bps_gross` | `(sell_bid − buy_ask) / buy_ask × 10 000` |
| Net bps | `spread_bps_net` | see the formula below; sign is on the number, colour is secondary |
| Liquidity (quote) | `liquidity_quote` | `min(buy_ask × buy_ask_qty, sell_bid × sell_bid_qty)` in quote currency; `unknown` when either side publishes no sizes |
| Lifetime (s) | `lifetime_s` | seconds since the lane first exceeded the request's threshold without dropping below it; `first_seen_at` in the detail drawer |
| Networks | `networks.buy_withdraw`, `networks.sell_deposit` | `open`, `closed` or `unknown`; hover shows the reason — `unknown (venue requires API key)` on every venue except those with a public currency endpoint |
| Buy age / Sell age | `buy_age_ms`, `sell_age_ms` | age of each side's quote against the poll interval |
| Detail | — | opens the row drawer: per-side venue, price × quantity, taker fee in bps, age; gross/net; first seen; lifetime; *Open in Calculator* |

The detail drawer's fee lines (`buy_fee_bps`, `sell_fee_bps`) are the
values from the venue fee table used for that row, so you can see what the
net number assumed.

## Net-of-fees formula

```
net_bps = (sell_bid × (1 − f_sell) − buy_ask × (1 + f_buy)) / buy_ask × 10 000
```

`f_buy` and `f_sell` are the buy and sell venues' regular-tier spot taker
fees from Settings → Scanner Suite, as fractions (10 bps = 0.001). Fees
are composed multiplicatively, never by adding bps. Token fee discounts are
off in every model. The transfer fee is not included — the row footer says
"no-transfer model, top-of-book liquidity, net of taker fees" — unless
you take the lane to the [calculator](calculator.md) and enter one.

Worked example from `docs/design/strategy-models.md` §2.5 (Binance as the
buy venue with its verified 10 bps spot taker; the second venue's 10 bps
is an unverified assumption): ask 50 000.00, bid 50 150.00 → gross 30.00
bps, net 9.97 bps. A rule that also subtracts a 2 bps slippage allowance
per leg and a 5 bps buffer arrives at 0.97 bps, below a 5 bps threshold —
a 30 bps gross spread on a 10/10 pair is not tradeable at retail taker
rates.

## What is hidden by default, and how to see it

Two classes of lane are computed but excluded from the table, the alert
evaluator and the paper executor unless you opt in. The response always
reports how many were hidden (`excluded.suspect`,
`excluded.liquidity_unknown`) and which filters applied.

### Suspect lanes (asset-identity guard)

The same ticker on two venues is not necessarily the same asset. A lane
is `suspect: true` when

- the two venues' mids differ by more than `max_plausible_spread_bps`
  (default 2000 = 20 %) — `suspect_reason: spread_exceeds_max_plausible`; or
- with three or more venues quoting the pair, either side's mid is more
  than 50 % away from the cross-venue median —
  `suspect_reason: price_deviates_from_median`.

Suspect rows have no lifetime, are skipped by rules and auto-paper with
reason `SUSPECT_MISMATCH`, and nothing about them is persisted; the verdict
is recomputed on every request. The guard is one function shared by the
table, the evaluator and the executor. `include_suspect=1` on the API
returns them flagged.

### Unknown liquidity

When either side's collector publishes no top-of-book sizes (Gate's bulk
spot ticker), the row carries `liquidity_quote: null` and
`liquidity_unknown: true`. An unknown is never shown as 0 and never
compared with a minimum. Such rows are hidden unless
`include_unknown_liquidity=1`, and rules and auto-paper skip them with
reason `LIQUIDITY_UNKNOWN`.

The console table today requests the safe defaults only; the two include
flags are API query parameters. A console toggle for them is **planned**.

## Filters

Filter card, above the table. Each maps to a query parameter except where
noted.

| Filter | Parameter | Notes |
|---|---|---|
| Buy on / Sell on | `buy`, `sell` (comma lists) | venue chips; empty = any enabled venue |
| Min spread (bps) | `min_spread_bps` | on the **net** figure |
| Min liquidity (quote) | `min_liquidity` | defaults to `settings.min_liquidity_quote` (500 on a fresh install) when blank |
| Min lifetime (s) | `min_lifetime_s` | lifetime is measured against this row's threshold |
| Quote asset | `quote` | console offers any / USDT / USDC / BTC / ETH; the API accepts any comma list, exact match |
| Base allow-list | `base` | comma list; the console sends it as the `base` parameter |
| Base deny-list | — | applied client-side to the fetched page (no wire slot in the v1 contract) |
| Limit | `limit` | the console asks for 200 rows |

## Saved templates

A template is a named copy of the current filter set. Templates are
**per user**: anyone with `screener:view` can load their own; the console
shows *Save current filters as* and the delete button only to users with
`screener:config` (the API itself allows any viewer to save and delete
their own). `GET/POST /api/v1/screener/templates`,
`DELETE /api/v1/screener/templates/{id}`. The number of templates you may
keep is a package limit (`rules.templates_max`).

## Data age

Each side of a lane carries its own age. The three bands, shared by every
Scanner Suite table:

- ≤ 1× poll interval: normal;
- 1–3×: warning colour;
- > 3×: the age cell reads `STALE <age>` and the rest of the row is
  dimmed (hovering restores full contrast).

Alert rules and auto-paper apply a stricter gate than the display: every
leg must be no older than one poll interval and the two legs no more than
half a poll interval apart, otherwise the lane is skipped with
`DATA_AGE`. A fresh leg against a stale leg manufactures phantom spreads.

## From a row to a number you can size

*Detail → Open in Calculator* carries base, quote and both venues to the
[calculator](calculator.md), where you enter a size, optional fee
overrides and an optional transfer fee.

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
