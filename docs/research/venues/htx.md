# HTX (Huobi) — public endpoint research (T-075)

Scope: PUBLIC endpoints only; spot (api.huobi.pro) + USDT-margined linear swaps (api.hbdm.com).
Access date for every citation: **2026-08-27**. Legend as in docs/research/screener-endpoints.md.
Collector: internal/screener/venue/htx.go. Fixtures: internal/screener/testdata/htx/.
The current HTX docs portal (htx.com/en-us/opend) is JavaScript-rendered; the official static mirror
huobiapi.github.io/docs was used and is cited per section.

## 1. Spot bulk ticker
- `GET /market/tickers` → `status`, `ts` (ms, top level), `data[] {symbol, bid, bidSize, ask, askSize, close, …}` (bid/ask fields
  "added 2020.03.12"). Values are BARE JSON numbers. **VERIFIED**
- Source: https://huobiapi.github.io/docs/spot/v1/en/#get-latest-tickers-for-all-pairs
- Live: 604 rows; every `state=online && te` symbol (600) has a ticker.

## 2. Spot instruments / status
- `GET /v2/settings/common/symbols` → `data[] {sc (symbol), bc (base), qc (quote), state online|offline|suspend|…, te (trade enabled),
  tpp (price precision), tap (amount precision), …}`; `ts` is a STRING here (a number on market endpoints). **VERIFIED**
- `minov` (min order value) is documented but absent from the live v2 rows → MinNotional "". **VERIFIED-ABSENT (live)**
- Currency codes are lower case (`btc`); only the case is normalised.
- Source: https://huobiapi.github.io/docs/spot/v1/en/#get-all-supported-trading-symbol-v2
- Live: 2160 symbols — 600 online, 1557 offline, 3 suspend.

## 3. USDT-M linear swaps
- Instruments: `GET /linear-swap-api/v1/swap_contract_info` → `symbol` (= base), `contract_code`, `trade_partition` (= USDT),
  `contract_size`, `price_tick`, `contract_status` (1 Ready, 2 Delivering, 3 Settlement, 4 Delivered, 5 Suspended, 6 Preparing,
  7 Suspending, 8 Suspended), `business_type` swap, `settlement_period` ("1"/"4"/"8" hours live). **VERIFIED**
  Source: https://huobiapi.github.io/docs/usdt_swap/v1/en/#get-swap-info
- Best bid/ask (bulk): `GET /linear-swap-ex/market/detail/batch_merged?business_type=swap` → `ticks[] {contract_code, bid [price,size],
  ask [price,size], ts}`. **VERIFIED** — https://huobiapi.github.io/docs/usdt_swap/v1/en/#get-a-batch-of-market-data-overview
- Funding (bulk): `GET /linear-swap-api/v1/swap_batch_funding_rate` → `funding_rate`, `estimated_rate`, `funding_time`, `next_funding_time`.
  **VERIFIED fields**; live: `estimated_rate` and `next_funding_time` null on all 309 rows; `funding_time` is the UPCOMING settlement
  (observed 17:09 UTC → 1787875200000 = 00:00 UTC next day) — **semantics VERIFIED by observation, doc wording UNVERIFIED**.
  Source: https://huobiapi.github.io/docs/usdt_swap/v1/en/#query-a-batch-of-funding-rate
- Index (bulk): `GET /linear-swap-api/v1/swap_index` → `index_price`, `index_ts`. **VERIFIED (live)**
- Mark price: **no bulk endpoint (VERIFIED-ABSENT** in the market-data endpoint list). Per contract:
  `GET /index/market/history/linear_swap_mark_price_kline?contract_code&period=1min&size=1` → `data[0].close` (live; doc names the
  array `tick`). The collector fetches it round-robin (`funding_calls_per_poll` per Perps() call) and carries last-known marks;
  a contract without a mark yet has Mark 0 and basis.go skips it.
- Funding history: `GET /linear-swap-api/v1/swap_historical_funding_rate?contract_code` → `data.data[] {funding_rate, realized_rate,
  funding_time}`. **VERIFIED (live)**
- Interval: `settlement_period` hours. **VERIFIED**

## 4. Currency / chain status — PUBLIC
- `GET /v2/reference/currencies` (public, "Read" permission only) → `currency`, `instStatus` normal|delisted,
  `chains[] {chain, depositStatus, withdrawStatus}` with values `allowed`|`prohibited` (live). **VERIFIED**
- Source: https://huobiapi.github.io/docs/spot/v1/en/#apiv2-currency-amp-chains

## 5. Rate limits — UNVERIFIED from a rendered page
- Derivatives docs ("API Rate Limit Illustration"): market interfaces "at most 800 times/1s per IP"; non-market public interfaces
  "240 times every 3 seconds per IP". The section did not render in fetches; wording taken from the official page's search excerpt. **UNVERIFIED**
- Spot: four endpoints (depth, kline, detail/merged, trade) limited to 100 req/10 s since 2022-03-01 — /market/tickers not itemised.
  https://www.htx.com/support/34899765028398 **VERIFIED for those four only**
- Collector gates: spot 2 req/s, swap 10 req/s (poll = 3 bulk calls + N mark klines).

## 6. Fees — UNVERIFIED
- USDT-M: "Fee Rates to Open: Maker 0.02 %; Taker 0.06 %" — stated as "for illustration only" on
  https://www.htx.com/support/900000089923; a 2020 announcement (https://www.htx.com/support/900001178146) says 0.02 % / 0.04 %.
  Placeholder taker 0.06 % (the higher figure). **UNVERIFIED**
- Spot regular tier: fee page (https://www.htx.com/fee/) did not render; placeholder 0.20 %. **UNVERIFIED**

## 7. Notes
- Envelopes: market `{"status":"ok"}` (+ `err-code`/`err-msg`), v2 reference `{"code":200}`.
- Connector complexity: MEDIUM (per-contract mark price).
