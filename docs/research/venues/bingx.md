# BingX — public endpoint research (T-078)

Scope: PUBLIC endpoints only; spot + USDT-M perpetuals. Access date for every citation:
**2026-08-27**. Legend as in docs/research/screener-endpoints.md.
Collector: internal/screener/venue/bingx.go. Fixtures: internal/screener/testdata/bingx/.
Docs: https://bingx-api.github.io/docs-v3/ (official "BingX API Docs V3", BingX-API GitHub org;
the site is a JS app — facts below were read from its content bundle
docs-v3/static/js/app.1297ec6437891215cd4e.js, which carries the verbatim EN field tables).
REST host: https://open-api.bingx.com.

## 1. Spot bulk ticker (bid/ask + size)
- `GET /openApi/spot/v2/quote/bookTicker`, "Symbol Order Book Ticker"; param symbol
  "Trading pair, such as BTC-USDT; omit to return all symbols". Response `data[]`:
  `time` ("Data update time, in milliseconds"), `symbol`, `bidPrice` ("Best bid price"),
  `bidQty` ("Best bid quantity"), `askPrice`, `askQty` — all strings. Signature Verification: No.
  **VERIFIED** (docs-v3 → Spot → Market Data → Symbol Order Book Ticker,
  https://bingx-api.github.io/docs-v3/#/en/spot/market-api)
- Live 2026-08-27: 797 rows without symbol param.

## 2. Spot instruments / status
- `GET /openApi/spot/v1/common/symbols`, "Spot trading symbols": `symbol` ("Trading pair, e.g.,
  BTC-USDT"), `status` int — documented enum "0 offline, 1 online, 5 pre-open, 10 accessed,
  25 suspended, 29 pre-delisted, 30 delisted", `tickSize` ("Price tick size"), `stepSize`
  ("Quantity step size"), `minNotional` ("Minimum trading notional"), `maxNotional`,
  `apiStateBuy` ("Buy allowed"), `apiStateSell` ("Sell allowed"). **VERIFIED**
  (docs-v3 → Spot → Market Data → Spot trading symbols)
- No base/quote fields exist (VERIFIED-ABSENT). The documented symbol format is hyphenated —
  spot: "Trading pair, e.g., BTC-USDT"; swap: 'There must be a hyphen/ "-" in the trading pair
  symbol. eg: BTC-USDT' — so base/quote are the two hyphen-separated halves of the venue's own
  instrument list entries (never guessed from an un-delimited string).
- Tradable = status 1 AND apiStateBuy AND apiStateSell. Live: 2259 symbols (1504 status 0,
  705 status 1, 39 status 10, 10 status 25, 1 status 5).
- Numbers here are BARE floats (tickSize 1e-05) — parsed from literal text via decimal.

## 3. USDT-M perpetuals
- Instruments: `GET /openApi/swap/v2/quote/contracts`, "USDT-M Perp Futures symbols":
  `symbol`, `asset` ("contract trading asset" = base), `currency` ("settlement and margin
  currency asset" = quote), `status` ("1 online, 25 forbidden to open positions, 5 pre-online,
  0 offline"), `pricePrecision` / `quantityPrecision` (decimal places), `tradeMinQuantity`,
  `tradeMinUSDT`, `makerFeeRate` / `takerFeeRate` (live 0.0002 / 0.0005 on BTC-USDT),
  `apiStateOpen`, `apiStateClose`. **VERIFIED** (docs-v3 → USDT-M Perp Futures → Market Data)
  Live: 1117 contracts (1049 USDT status 1, 49 USDC, 19 USDT status 25). USDT-margined only kept.
- Bulk best bid/ask: `GET /openApi/swap/v2/quote/ticker`, "24hr Ticker Price Change Statistics"
  (symbol omitted → all): `bidPrice`, `bidQty`, `askPrice`, `askQty`, `closeTime`. **VERIFIED**
- Bulk mark/index/funding: `GET /openApi/swap/v2/quote/premiumIndex`, "Mark Price and Funding
  Rate" (symbol omitted → all): `markPrice` ("current mark price"), `indexPrice` ("index price"),
  `lastFundingRate` ("Last updated funding rate"), `nextFundingTime` (ms); the documented example
  response also carries `fundingIntervalHours` (8 in the doc example; live 632×8h, 459×4h, 7×1h).
  **VERIFIED** (field list) / fundingIntervalHours **VERIFIED via documented example response**.
- Funding history: `GET /openApi/swap/v2/quote/fundingRate?symbol=&limit=`, "Get Funding Rate
  History": `fundingRate`, `fundingTime` (ms), limit default 100 max 1000. **VERIFIED** (not polled)
- No predicted/next-interval rate is published (VERIFIED-ABSENT) → PredictedFundingRate zero.

## 4. Currency / chain status — key-gated
- The wallet deposit/withdraw config (`/openApi/wallets/v1/capital/config/getall`) is an
  authenticated endpoint; no public equivalent → "unknown (venue requires API key)". **VERIFIED**

## 5. Rate limits / bans
- Market-data endpoints above are all rate-limit group 1: "IP Rate Limit :500 requests per
  10 seconds." (docs-v3 renders this banner for every endpoint in the group). **VERIFIED**
- Error-code reference: 429 "Too Many Requests — Requests are too frequent, rate-limited by the
  system."; 418 "Continued access after receiving 429, IP has been banned. Stop requests and
  wait."; base info: "If requests are too frequent, the system will automatically rate-limit them
  and restore after 5 minutes." **VERIFIED**
- Collector gate: 100 requests / 10 s per venue (single host).

## 6. Fees (regular tier)
- Perp: "Taker (filled instantly): 0.05% | Maker (pending orders): 0.02%" — official support
  article "Perpetual Futures | Fee Schedule",
  https://bingx.com/en/support/articles/360046487573-perpetual-futures-fee-schedule; matches the
  contracts API `takerFeeRate` 0.0005 on BTC-USDT. **VERIFIED (perp taker 5 bps)**
- Spot: the official "Fee Schedule" article
  (https://bingx.com/en/support/articles/360027240173-fee-schedule) defers to
  https://bingx.com/support/costs/ which renders only via JS (fetched 2026-08-27, no table in
  HTML) → spot taker **UNVERIFIED**, placeholder 10 bps. Registry Verified=false (spot side).

## 7. Notes
- Envelope `{code, msg|timestamp, data}`; code 0 = success.
- Spot quote fields are strings in v2 bookTicker; contract fee/precision fields are bare numbers.
- Connector complexity: LOW-MEDIUM (all bulk; hyphen symbol format documented).
