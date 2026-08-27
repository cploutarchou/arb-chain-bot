# Perpetuals and funding

Console pages **Scanner Suite → Perpetuals** and **Scanner Suite →
Funding**; API `GET /api/v1/screener/perpetuals` and
`GET /api/v1/screener/funding` (`screener:view`).

**Information only.** Perpetual futures are leveraged derivatives.
Availability to you depends on your country and your venue's rules, not on
this product; in several jurisdictions retail access to crypto derivatives
is restricted or banned. These pages show public market data and a model;
they do not suggest opening a position.

## Perpetuals page

One row per (venue, base) where the venue publishes a USDT-margined (or
venue-native) perpetual **and** its own spot book for the same base.
Basis always uses that venue's own spot mid; a borrowed mid from another
venue would misprice the basis, so venues screened only for perps have no
row.

| Column | Wire field | Meaning |
|---|---|---|
| Venue, Base | `venue`, `base` (`quote` on the wire) | |
| Spot mid | `spot_mid` | `(bid + ask) / 2` of the venue's spot book — display and basis only, never a PnL input |
| Perp mark, Perp index | `perp_mark`, `perp_index` | the venue's mark and index prices |
| Basis bps | `basis_bps` | `(perp_mark − spot_mid) / spot_mid × 10 000` |
| Funding rate | `funding_rate` | the venue's current per-interval rate, as published (fraction; 0.0001 = 1 bps) |
| Predicted | `predicted_funding_rate` | the venue's forecast for the next interval where it publishes one; on Binance and Bybit the current-interval rate is reported in both columns because the bulk endpoint has no separate field |
| Interval (h) | `funding_interval_h` | funding interval in hours (see the per-venue table below) |
| Next funding | `next_funding_at` | countdown to the next settlement; `due` once it passes |
| Carry APR gross | `carry_apr_gross` | see below |
| Carry APR net | `carry_apr_net` | gross minus the fee amortisation below |
| Age | `age_ms` | perp snapshot age against the poll interval |

Rows are sorted by net carry, descending. Filters: venue chips
(client-side; the wire `venue` parameter is a single value), base, and a
minimum net carry.

**Known unit mismatch.** The console's *Min carry APR* field is labelled
"% APR" but the value is sent unchanged as `min_carry_apr`, and the API
compares it with `carry_apr_net`, which is a **fraction** (0.10 = 10 %).
Until the console converts, type the fraction (`0.1`, not `10`).

## Carry APR

```
carry_apr_gross = funding_rate × (24 / interval_h) × 365
fee_apr         = (2 × spot_fee + 2 × perp_fee) × 365 / 30
carry_apr_net   = carry_apr_gross − fee_apr
```

- `funding_rate` is the per-interval fraction the venue publishes. The
  annualised figure is display only: it multiplies today's rate out for a
  year and is not a forecast.
- Reference point: `0.0001` per 8 h (Binance's neutral rate) annualises to
  `0.1095` = 10.95 % gross — that is zero premium over neutral, not an
  edge.
- The net figure amortises the four taker fees of a round trip (buy spot,
  sell perp, later sell spot, buy perp) over an **assumed 30-day hold**.
  The assumption is a constant (`HoldDaysAssumed = 30`) echoed on every row
  as `hold_days_assumed` and printed in the page footer, so the console
  never hardcodes it. It is a modelling assumption for the display; the
  auto-paper carry strategy has its own real close-on-convergence,
  max-hold and stop logic (see [Auto-paper](auto-paper.md)).
- Spot and perp fees are the venue's regular-tier taker rates from
  Settings → Scanner Suite (`spot_fee_bps`, `perp_fee_bps` on the row).
  Whether they are verified is listed in [Venues](venues.md).
- The footer states the hold assumption, that funding accrues every
  interval, that basis convergence is not assured, and the no-transfer,
  top-of-book, net-of-taker-fee model.

Funding rates change sign; predicted funding is not settled funding; a
basis can widen before it narrows. Nothing on the row is a promise that
any of it persists for the assumed hold.

## Funding intervals per venue

Intervals are re-read on every poll; a venue may change a contract's
interval (Binance switches to 1 h when a rate hits its cap in extreme
volatility and reverts after 16 quiet intervals).

| Venue | Where the interval comes from | Current / predicted rate |
|---|---|---|
| Binance | `fundingInfo` (bulk) lists only symbols adjusted away from 8 h; absent = 8 h | `lastFundingRate` from `premiumIndex` is the accruing rate, reported as both |
| OKX | **per instrument**: derived as `nextFundingTime − fundingTime` from `/public/funding-rate` (8 h / 4 h / 2 h / 1 h per instrument, no dedicated field); mark price from a separate endpoint | `fundingRate` and `nextFundingRate`, per instrument, round-robin `funding_calls_per_poll` per poll; others carry their last-known values |
| Bybit | `fundingInterval` (minutes) on the linear instrument list | one field, reported as both |
| Bitget | `fundInterval` (hours) on the contract list; `fundingRateInterval` (1/2/4/8 h) per symbol | per symbol, round-robin |
| Gate | `funding_interval` (seconds) on the contract list | `funding_rate` and `funding_rate_indicative` from the bulk futures ticker |
| MEXC | `collectCycle` (hours) per symbol | `fundingRate` in the bulk contract ticker; `nextSettleTime` per symbol, round-robin |
| KuCoin | `fundingRateGranularity` (ms) on the active-contracts list | `fundingFeeRate` and `predictedFundingFeeRate`, bulk |
| HTX | `settlement_period` (hours) on the contract list | `swap_batch_funding_rate` bulk (`estimated_rate` may be null); mark price per contract, round-robin |
| Kraken | settlement every 1 hour (USD-quoted multi-collateral perps, no USDT-margined contract) | `fundingRate` and `fundingRatePrediction`, bulk; relative rate used |
| Coinbase | no perpetuals in public Advanced Trade data — no rows | — |

## Funding page

- **Current funding rates (venue × base)** — a grid built from the same
  rows the Perpetuals page shows (there is no separate grid route); a dash
  means that venue has no perp row for that base.
- **72 h funding history** — one line per venue for the selected base,
  from `GET /screener/funding?base=&hours=72`.

History rows come from `funding_history`: the poller writes one row per
(venue, base, settlement time) when it observes a contract's next-funding
time advance, recording the rate that was reported before the advance as
the settled rate. Auto-paper books funding from these settled rows, never
from a predicted rate. A venue that was offline over a settlement has a
gap, not an interpolated value.

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
