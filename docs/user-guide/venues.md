# Venues

The Scanner Suite screens ten venues through **public** REST market data
only — no API key, no signed request, no read of the secrets vault. Every
endpoint, field, limit and fee a collector relies on is cited to the
venue's official documentation with an access date in the collector's
source and in `docs/research/screener-endpoints.md` (Tier-1) and
`docs/research/venues/<venue>.md` (Tier-2), or is marked UNVERIFIED and
not relied on. Access date for everything below: 2026-08-27.

## The ten venues

| Venue | Tier | Enabled by default | Spot sizes in bulk | Perps | Fees verified | Deposit/withdraw status |
|---|---|---|---|---|---|---|
| Binance | 1 | yes | yes | USDⓈ-M | **yes** — spot taker 0.100 %, USDⓈ-M taker 0.050 % from the official fee pages | unknown (requires API key) |
| OKX | 1 | yes | yes | SWAP (linear) | no — fee page did not render; 10 bps / 5 bps are aggregator figures | unknown (requires API key) |
| Bybit | 1 | yes | yes | linear | no — fee page timed out; 10 bps / 5.5 bps | unknown (requires API key) |
| Bitget | 1 | yes | yes | USDT-FUTURES | no — 10 bps / 6 bps | unknown (treated as key-gated) |
| Gate | 1 | yes | **no** — bulk spot ticker has no bid/ask sizes | USDT futures (sizes present) | no — 20 bps / 5 bps | **public** per-chain deposit/withdraw flags |
| MEXC | 1 | yes | yes | USDT-M | no — promos vary; 5 bps / 2 bps | unknown (requires API key) |
| KuCoin | 2 | no (opt-in after soak) | yes | USDT-M (base `XBT` mapped to `BTC`) | **yes** — spot 0.1 %, futures 0.06 % from the API's own fee fields and the futures fee announcement | **public** per-chain flags |
| HTX | 2 | no | yes | linear swap | no — 20 bps / 6 bps ("illustration only" figure) | **public** per-chain flags |
| Kraken | 2 | no | yes | Kraken Futures, **USD-quoted** multi-collateral, 1 h funding | **yes** — Pro spot tier 1 maker 0.40 % / taker 0.80 % (settings carry the taker, 80 bps; kraken.com/features/fee-schedule, 2026-08-27), futures 0.05 % | unknown (requires API key) |
| Coinbase | 2 | no | **no bulk ticker** — per-product order books, round-robin | **none** in public Advanced Trade data | no — 120 bps taker on the entry tier, unverified (fee pages answer 403) | network status only, no deposit/withdraw flags |

"Fees verified" means both the spot and the perp taker rate were read from
the venue's official fee schedule. An unverified venue still runs; its net
numbers use whatever the settings document says, and the venue registry
flags it `Verified=false`. The production gate's model-honesty item
requires that no unverified fee be in the executed venue set — it is a
manual check, and a reason the gate cannot pass on those venues today.

The Kraken spot default (80 bps) is the tier-1 *taker* rate from the
official fee schedule (maker is 0.40 %); it is the highest spot taker fee
among the verified venues, so Kraken lanes need the widest gross spread
to net positive.

## What each venue cannot publish, and how the suite handles it

- **Gate spot sizes.** The bulk `/spot/tickers` response carries prices
  only ("not available for batch queries"). Gate spot quotes are
  `LiquidityUnknown`; their lanes are hidden from the table by default,
  never shown as zero, and skipped by rules and auto-paper as
  `LIQUIDITY_UNKNOWN`. Gate futures tickers do carry sizes. Gate's bulk
  spot ticker may also return empty strings for bid or ask on thin pairs;
  those rows are dropped for that poll.
- **Coinbase.** The public products list has empty best bid/ask, so the
  collector fetches `product_book?limit=1` for `BooksPerPoll` products per
  poll (default 40) round-robin, and every other product keeps its
  last-known quote **with its own timestamp** — ages stay honest and a
  large share of Coinbase rows will read stale at any moment. Coinbase has
  no perpetuals in this data; `perps_enabled` is off and cannot produce
  rows.
- **OKX funding.** Current and predicted funding, and the interval, come
  from a per-instrument endpoint; the collector refreshes
  `funding_calls_per_poll` instruments per poll (default 10) and the rest
  carry their last-known values. Mark price is a separate bulk call.
  Expect funding fields on OKX to be older than the quote fields.
- **Bitget and MEXC funding.** Same round-robin for the next-settlement
  time and per-symbol interval; the rate itself is in the bulk ticker.
- **HTX mark price.** No bulk endpoint; per-contract, round-robin. A
  contract with no mark yet has no basis row.
- **Kraken perps.** USD-quoted, hourly funding; the relative rate is used.
  There is no USDT-margined contract, so Kraken basis rows are `USD`
  quote, distinct from `USDT` rows elsewhere.
- **Deposit/withdraw status.** Only Gate, KuCoin and HTX publish it
  without a key; Coinbase publishes network status without deposit or
  withdraw flags. Everywhere else the column says `unknown (venue requires
  API key)` and is never inferred from another source.
- **Symbols.** Base and quote come from each venue's instrument list,
  never from splitting the symbol string. Contracts without a resolvable
  spot pair (Gate) are skipped rather than guessed.

## Rate limits and polling

Each venue has its own request gate, set at or below half the documented
limit so the triangular engine's own Binance REST use still fits. The
collectors never poll faster than every 2 s regardless of settings. Limits,
ban codes and what to do about them are in
`docs/runbooks/screener-collectors.md`.

## Planned venues

Crypto.com, Bitfinex, BingX, Upbit, Bithumb, WhiteBIT, LBank, BitMart and
Phemex are on the Tier-2 list but not built. DEX aggregators (Desk
package) are **planned**.

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
