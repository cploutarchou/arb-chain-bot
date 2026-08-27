# WhiteBIT — public endpoint research (T-078)

Scope: PUBLIC endpoints only; spot + USDT-M perpetuals. Access date for every citation:
**2026-08-27**. Legend as in docs/research/screener-endpoints.md.
Collector: internal/screener/venue/whitebit.go. Fixtures: internal/screener/testdata/whitebit/.
Docs: https://docs.whitebit.com (source: github.com/whitebit-exchange/api-docs
pages/public/http-v4.mdx and http-v1.mdx — the site renders those files).
REST host: https://whitebit.com.

## 1. Spot bulk ticker (bid/ask, NO sizes)
- `GET /api/v1/public/tickers` — "information about recent trading activity on all markets";
  `result[market].ticker`: `bid` ("Highest bid"), `ask` ("Lowest ask"), `last`, `vol`, `deal`;
  `at` timestamp in seconds. Rate limit 1000 requests/10 sec; response cached 1 s. **VERIFIED**
- Source: https://docs.whitebit.com/public/http-v1/ § Market Activity
- No size fields (VERIFIED-ABSENT; per-market `/api/v4/public/orderbook/{market}` exists at
  600 req/10 s but is one request per market) → quotes carry `LiquidityUnknown`, sizes zero,
  like Gate. The v4 bulk `/public/ticker` has no bid/ask at all (only last_price/volumes).

## 2. Instruments / status (spot + futures market list)
- `GET /api/v4/public/markets` — "all information about available spot and futures markets":
  `name`, `stock` ("Ticker of stock currency" = base), `money` ("Ticker of money currency" =
  quote), `stockPrec` / `moneyPrec` (currency precisions), `makerFee` / `takerFee` ("Default
  maker/taker fee ratio"), `minAmount`, `minTotal` ("Minimal amount of money to trade"),
  `tradesEnabled` ("Is trading enabled"), `type` "spot" | "futures". Rate limit 2000 req/10 s.
  **VERIFIED** — https://docs.whitebit.com/public/http-v4/ § Market Info
- Live 2026-08-27: 1199 markets — 801 spot, 305 futures, 93 type "tradfiFutures" (not in the
  documented enum; skipped). TickSize/StepSize derived as 10^-moneyPrec / 10^-stockPrec (the live
  `tickSize`/`stepSize` fields are NOT in the documented response → UNVERIFIED, unused).

## 3. USDT-M perpetuals
- `GET /api/v4/public/futures` — "list of available futures markets"; `result[]`: `ticker_id`
  ("Identifier of a ticker with delimiter to separate base/target", = the market name, e.g.
  BTC_PERP), `stock_currency` ("Symbol/currency code of base pair"), `money_currency`
  ("Symbol/currency code of target pair", live all USDT), `bid` ("Current highest bid price"),
  `ask` ("Current lowest ask price"), `product_type` ("What product is this? Futures, Perpetual,
  Options?"; live all "Perpetual"), `index_price` ("Underlying index price"), `funding_rate`
  ("The current funding rate"), `next_funding_rate_timestamp` (ms), `funding_interval_minutes`
  ("Funding interval in minutes"; doc example 300, live 240). Rate limit 2000 req/10 s. **VERIFIED**
- Source: https://docs.whitebit.com/public/http-v4/ § Available Futures Markets List
- MARK PRICE: the live response carries `mark_price` but the documented response does NOT →
  **UNVERIFIED, unused**; Perp.Mark stays 0 with Index set (conformance class noBulkMark).
- No bid/ask sizes on the futures list (VERIFIED-ABSENT) → perp Bid/Ask carried, sizes n/a.
- No predicted rate (VERIFIED-ABSENT) → PredictedFundingRate zero.
- IntervalH = funding_interval_minutes / 60 (0 when not a whole hour).
- Funding history: `GET /api/v4/public/funding-history/{market}?limit=` → `fundingTime` (s),
  `fundingRate`, `settlementPrice`, `rateCalculatedTime`. **VERIFIED** (not polled)

## 4. Currency / chain status — PUBLIC
- `GET /api/v4/public/assets`: per asset `can_deposit` ("Identifies whether deposits are enabled
  or disabled"), `can_withdraw`, plus `networks.deposits[]` / `networks.withdraws[]` and
  per-network `limits`. Rate limit 2000 req/10 s. **VERIFIED**
- Source: https://docs.whitebit.com/public/http-v4/ § Asset status list
- Open when can_deposit AND can_withdraw; closed otherwise (reason says which side).

## 5. Rate limits / bans
- Per-endpoint limits are printed on every section (see above; 1000–2000 req/10 s for everything
  polled). FAQ: "If the rate limit for an endpoint is exceeded, you will receive a 429 error …
  Wait for the rate limit window to reset." No ban wording. **VERIFIED**
  (https://docs.whitebit.com/faq)
- Collector gate: 100 requests / 10 s.

## 6. Fees (regular tier) — UNVERIFIED (unit ambiguity)
- The markets endpoint documents `takerFee` as "Default taker fee ratio" with example "0.001",
  but live returns "0.1" for spot markets and "0.055" for BTC_PERP, while the assets endpoint
  documents `taker_fee` as "Taker fee in percentage" (live "0.1"). Percent semantics ("0.1" =
  0.10 %, "0.055" = 0.055 %) are consistent across both live endpoints, but the markets doc
  example contradicts it, and https://whitebit.com/fee-schedule answers HTTP 403 to fetchers →
  **UNVERIFIED**: placeholders spot taker 10 bps, perp taker 5.5 bps; Registry Verified=false.

## 7. Notes
- v1 envelope `{success, message, result}`; v4 endpoints return bare JSON (futures wraps in
  `{success, result}`).
- All prices are decimal strings.
- Connector complexity: LOW (all bulk; base/quote explicit on both lists).
