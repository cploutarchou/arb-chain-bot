# Spreads calculator

Console page **Scanner Suite → Calculator**; API
`POST /api/v1/screener/calculator` (`screener:view` plus CSRF — it is a
POST, but it persists nothing and is a read of the live book).

The calculator takes one lane and a size and returns the backend's own
net result against the **current** top of book. Opening it from a
Screener row (*Detail → Open in Calculator*) prefills base, quote, buy
venue and sell venue; everything else you type.

## Inputs

| Field | Wire | Notes |
|---|---|---|
| Base asset, Quote asset | `base`, `quote` | upper-cased; quote defaults to USDT |
| Buy venue, Sell venue | `buy_venue`, `sell_venue` | any of the ten known venues; the venue must be enabled and have a quote for the pair or the call fails |
| Size (quote) | `size_quote` | notional you want to deploy, in quote currency; default 1000 |
| Transfer fee (quote, optional) | `transfer_fee_quote` | blank = the no-transfer model; enter your own withdrawal fee in quote currency. Withdrawal fees per network are unverified for every venue and are never looked up |
| Override buy fee / sell fee (bps, optional) | `override_fees.buy_fee_bps`, `override_fees.sell_fee_bps` | replace the venue fee table for this calculation only, e.g. to test a VIP tier |

## Result

| Stat | Wire | Meaning |
|---|---|---|
| Buy ask, Sell bid | `buy_ask`, `sell_bid` | the quotes used |
| Size (base) | `size_base` | `size_quote / buy_ask`, truncated to step |
| Gross | `gross` | `size_base × (sell_bid − buy_ask)` in quote |
| Buy fees, Sell fees | `fees_buy`, `fees_sell` | taker fee on each leg at full precision |
| Transfer fee | `transfer_fee` | what you entered, or 0 |
| Net | `net` | `gross − fees_buy − fees_sell − transfer_fee` |
| Net bps | `net_bps` | `net / size_quote × 10 000` |
| Liquidity | `liquidity_ok` | `sufficient` when both top-of-book sizes cover `size_base`; `insufficient` otherwise. The calculator does not walk deeper levels — a larger order would pay more than shown |

The calculator does not apply the per-rule slippage allowance, buffer or
latency model that alert rules and auto-paper use; it is the arithmetic of
one instant, with the fees you chose. Quotes move; the result is a
measurement at request time, not what an order would achieve.

If the screener component is not running in this build the page shows
"Screener backend not available in this build."

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
