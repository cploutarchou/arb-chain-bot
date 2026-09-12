# Phemex — public endpoint research (T-075)

Scope: PUBLIC (no API key) endpoints only; spot + USDT-M perpetuals.
Access date for every citation: **2026-09-13**. Legend as in
docs/research/screener-endpoints.md (VERIFIED / VERIFIED-ABSENT /
UNVERIFIED). Planned collector: internal/screener/venue/phemex.go.
Official docs: phemex-docs.github.io (one long page — the USDⓈ-M/spot
market-data section paths below are live-verified even where the
rendered page truncated in fetch).

## 1. Bulk tickers (bid/ask, no sizes)
- USDT-M perps: `GET /md/v3/ticker/24hr/all` → `result:[{symbol
  "BTCUSDT", bidRp, askRp, lastRp, markRp, indexRp, highRp, lowRp,
  openRp, fundingRateRr, predFundingRateRr, openInterestRv,
  turnoverRv, timestamp (ns), …}]` — all perps in ONE request;
  `Rp`/`Rr` fields are JSON **strings** (parse as decimal), bid/ask
  included, sizes not. **VERIFIED** (live)
- Spot: `GET /md/spot/ticker/24hr/all` → `result:[{symbol "sBTCUSDT",
  bidEp, askEp, lastEp, indexEp, highEp, lowEp, openEp, turnoverEv,
  volumeEv, timestamp (ns)}]` — `Ep`/`Ev` fields are scaled integers:
  divide by 10^`priceScale` (per product, §2) for prices and by
  10^`valueScale` of the base currency for sizes/values. **VERIFIED**
  (live; sBTCUSDT `bidEp 7720876000000` = 77208.76 at priceScale 8)
- Per-symbol variants `?symbol=` exist (`sBTCUSDT` spot — the **`s`
  prefix is part of the spot symbol**; bare `BTCUSDT` is the perp) and
  return the same row alone. **VERIFIED** (live)

## 2. Instruments
- `GET /public/products` → `data:{currencies[], products[],
  perpProductsV2[], …}`. Spot `products[]`: `{symbol:"sBTCUSDT", type:
  "Spot", baseCurrency, quoteCurrency, priceScale, ratioScale,
  baseTickSizeEv, quoteTickSizeEv, minOrderValueEv, …}`. Perp
  `perpProductsV2[]`: `{symbol:"BTCUSDT", type:"PerpetualV2",
  baseCurrency, quoteCurrency, settleCurrency, contractSize, lotSize,
  tickSize, indexSymbol, markSymbol, fundingRateSymbol
  ".BTCUSDTFR", fundingRate8hSymbol ".BTCUSDTFR8H", …}`.
  `currencies[]` carries `valueScale` (the `Ev` divisor) and
  `status:"Listed"`. **VERIFIED** (live: 1018 spot products — 975
  USDT-quoted; 885 perpProductsV2 — 875 USDT-settled)
- Filters (plan): spot = `quoteCurrency == "USDT"`; perps =
  `settleCurrency == "USDT"` (drops COIN-M USD-quote perps like
  BTCUSD, which settle BTC).

## 3. Top-of-book sizes
- Spot book: `GET /md/spot/orderbook?symbol=sBTCUSDT` →
  `result:{book:{asks:[[priceEp, sizeEv]…], bids:[…]}, ts}` — scaled
  integers again, best level first. **VERIFIED** (live, per-symbol)
- Perp book: `/md/v3/recent`, `/md/v3/orderbook`, `/md/v3/book` all
  answer `6001 invalid argument` from this environment; the working
  perp book path was not identified. **UNVERIFIED — perp sizes stay
  ZERO and the lane reports LIQUIDITY_UNKNOWN (the Perp policy in
  types.go: never pretend a bound exists)**
- Design: spot ships with `LiquidityUnknown` unless/until a bounded
  per-symbol book sweep is added (same honest state Gate ships with);
  the bulk tickers are the primary source.

## 4. Funding
- History: `GET /api-data/public/data/funding-rate-history?symbol=
  .BTCUSDTFR8H&limit=` → `data:{rows:[{symbol, fundingRate
  "0.00005152", fundingTime (ms), intervalSeconds:28800}]}` — the
  symbol is the product's `fundingRate8hSymbol`, NOT the trading
  symbol. **VERIFIED** (live; `symbol=BTCUSDT` returns empty rows —
  live-verified wrong key)
- Interval: 8 h (`intervalSeconds 28800`, live rows 3×/day apart).
  **VERIFIED**
- Current/predicted rate ride on the §1 perp ticker
  (`fundingRateRr`, `predFundingRateRr`). **VERIFIED**

## 5. Rate limits / bans
- **5 000 requests / 5 min per IP** (block on excess), plus per-group
  capacities: contract 500/min, SpotOrder 500/min, Others 100/min
  (`/md` endpoints skip the code/msg/data wrapper and carry
  `x-ratelimit-remaining-<group>` headers). **VERIFIED** (docs
  overview)
- Collector gates (plan): Others-group-aware — 2 bulk tickers + 1
  products refresh per poll is trivially inside every window.

## 6. Fees (regular tier)
- Official help centre (phemex.com/help-center/phemex-trading-fee-
  structure): VIP0 spot **maker 0.1000 % / taker 0.1000 %**; VIP0
  contracts **maker 0.0100 % / taker 0.0600 %**. **VERIFIED**
- PT-token discounts (−20 % spot maker / −10 % USDT perp) are opt-in
  for individual traders — NOT applied (same rule as Bithumb's opt-in:
  the fee table carries the standard tier).

## 7. Notes
- `/md` responses use the JSON-RPC envelope `{error, id, result}`;
  `/api-data` and `/public` use `{code, msg, data}` (`code 0` = OK).
- Spot symbol's leading `s` must be stripped when mapping to the
  repo's instrument identity; the perp symbol needs no change.
- COIN-M perps exist (`/md/v1/...`, symbols like `.BTCFR8H`) — out of
  scope: the screener lanes are USDT-settled.
- Connector complexity: MEDIUM (bulk tickers yes; scaled-integer
  decoding with per-product scales; sizes deferred).
