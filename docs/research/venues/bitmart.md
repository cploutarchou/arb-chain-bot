# BitMart — public endpoint research (T-078)

Scope: PUBLIC endpoints only; spot + USDT-M perpetuals. Access date for every citation:
**2026-08-27**. Legend as in docs/research/screener-endpoints.md.
Collector: internal/screener/venue/bitmart.go. Fixtures: internal/screener/testdata/bitmart/.
Docs: https://developer-pro.bitmart.com/en/spot/ and /en/futuresv2/.
REST hosts: spot https://api-cloud.bitmart.com, futures https://api-cloud-v2.bitmart.com.

## 1. Spot bulk ticker (bid/ask + size)
- `GET /spot/quotation/v3/tickers` — "Get all trading pairs with a volume greater than 0 within
  24 hours … latest transaction price, best bid price, best ask price". Response `data` is an
  array of POSITIONAL arrays: `[symbol, last, v_24h, qv_24h, open_24h, high_24h, low_24h,
  fluctuation, bid_px ("top buy price"), bid_sz ("Size of top buy order"), ask_px
  ("top sell price"), ask_sz, ts (ms)]`. **VERIFIED**
- Source: https://developer-pro.bitmart.com/en/spot/ § Get Ticker of All Pairs (V3)
- Caveat: only pairs with 24 h volume > 0 appear (documented), so a zero-volume tradable pair has
  no quote until it trades.

## 2. Spot instruments / status
- `GET /spot/v1/symbols/details`: `symbol`, `base_currency`, `quote_currency`, `quote_increment`
  ("The minimum order quantity is also the minimum order quantity increment"), `base_min_size`
  ("Minimum order quantity"), `price_max_precision` ("Maximum price accuracy (decimal places)"),
  `min_buy_amount` ("Minimum order amount") / `min_sell_amount`, `trade_status`
  ("trading=is trading, pre-trade=pre-open"). **VERIFIED**
- Source: https://developer-pro.bitmart.com/en/spot/ § Get Trading Pair Details (V1)
- TickSize derived as 10^-price_max_precision; StepSize = quote_increment (the doc's own text says
  it is the base-quantity increment despite the name); MinNotional = min_buy_amount.

## 3. USDT-M perpetuals
- Instruments + index + funding in ONE call: `GET /contract/public/details` (symbol omitted → all):
  `symbol`, `product_type` (Int, "1=perpetual, 2=futures"), `base_currency`, `quote_currency`,
  `index_price`, `contract_size`, `price_precision` (live a decimal string, e.g. "0.1" — used as
  TickSize), `vol_precision`, `min_volume`, `funding_rate` ("current funding rate" — the field
  table for funding-rate endpoints calls rate_value "Funding rate of the previous period"),
  `expected_funding_rate`, `funding_interval_hours` (Int, "Funding interval"), `status`
  ("-Trading -Delisted"), `last_price`. **VERIFIED**
- Source: https://developer-pro.bitmart.com/en/futuresv2/ § Get Contract Details
  (12 times/2 sec per IP). Live 2026-08-27: 1215 contracts — 354 Trading+USDT perpetual,
  833 Delisted, 4 USD + 1 USDC Trading (skipped: quote must be USDT); intervals 8h×674,
  4h×523, 1h×16, 2h×2. The live rows also carry `funding_time` — NOT in the documented field
  table → unused; next-funding time comes from funding-rate-v2 below.
- Bulk current funding + next settlement: `GET /contract/public/funding-rate-v2` (symbol omitted →
  all; "Applicable to query the current fund rate of all trade pairs"): `rate_value` ("Funding
  rate of the previous period"), `expected_rate` ("Funding rate for the next period"),
  `funding_time` ("Next funding settlement time", ms), `funding_upper_limit`/`funding_lower_limit`.
  12 times/2 sec. **VERIFIED** — live covers a subset (96 symbols); the rest keep details' rates
  with NextFundingAt zero.
- MARK PRICE: no bulk endpoint. `GET /contract/public/markprice-kline?symbol=&step=1&start_time=&
  end_time=` ("Applicable for querying MarketPrice K-line data"): rows `{timestamp (s),
  open_price, close_price, high_price, low_price}`; 12 times/2 sec. **VERIFIED** → round-robin
  FundingCallsPerPoll contracts per poll (close_price of the last row = current mark), last-known
  carried; Mark 0 + Index>0 for unvisited contracts (conformance class noBulkMark, like HTX).
- Perp bid/ask: only per-symbol `/contract/public/depth` exists (VERIFIED-ABSENT in bulk) →
  Bid/Ask stay 0 (allowed: conformance requires them non-negative only).
- Funding history: `GET /contract/public/funding-rate-history?symbol=&limit=` → `funding_rate`
  ("Actual funding rate"), `funding_time` (ms). **VERIFIED** (not polled)

## 4. Currency status — PUBLIC (platform-level, no per-chain detail)
- `GET /spot/v1/currencies` (8 times/2 sec): `id`, `name`, `deposit_enabled` ("Whether this
  currency can be deposited on the platform"), `withdraw_enabled`. **VERIFIED**
- Source: https://developer-pro.bitmart.com/en/spot/ § Get Currency List (live 3710 currencies).
- Open when both true; closed otherwise (reason says which side). No chain granularity published.

## 5. Rate limits / bans
- "The speed of the public interface is limited according to the IP … When the requests exceed the
  rate limit, the 429 status will be returned: the request is too frequent." Spot table:
  /spot/quotation/v3/tickers 10 times/2 sec; /spot/v1/symbols/details 12 times/2 sec;
  /spot/v1/currencies 8 times/2 sec. Futures table: /contract/public/details, funding-rate-v2,
  markprice-kline each 12 times/2 sec. **VERIFIED**
- "HTTP 429 … the access frequency is overrun and the IP will be blocked. HTTP 418 … the IP has
  been blocked after error code 429." (both spot and futures FAQ/status-code pages). **VERIFIED**
- Collector gates: 4 requests / 2 s per host (spot and futures each).

## 6. Fees (regular tier) — UNVERIFIED
- Official fee page https://www.bitmart.com/en-US/fee (2026-08-27): futures "All Users —
  Maker 0.0400% / Taker 0.0600%"; spot fees are per Class A/B/C/D and the class table shows
  "No Data" unless signed in, and the futures table carries a second unexplained "0.2500%"
  column → the pair (spot taker, perp taker) is not fully readable without an account →
  **UNVERIFIED**: placeholders spot taker 25 bps, perp taker 6 bps; Registry Verified=false.

## 7. Notes
- Envelope `{code: 1000, message, data, trace}`; code 1000 = success.
- Spot ticker rows are positional arrays of strings; contract numbers are decimal strings.
- Connector complexity: MEDIUM (positional spot rows; per-symbol mark price round-robin).
