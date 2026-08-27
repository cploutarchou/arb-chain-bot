# KuCoin — public endpoint research (T-075)

Scope: PUBLIC (no API key) endpoints only; spot + USDT-margined perpetuals.
Access date for every citation: **2026-08-27**. Legend as in
docs/research/screener-endpoints.md (VERIFIED / VERIFIED-ABSENT / UNVERIFIED).
Collector: internal/screener/venue/kucoin.go. Fixtures: internal/screener/testdata/kucoin/.

## 1. Spot bulk ticker (bid/ask + size)
- `GET https://api.kucoin.com/api/v1/market/allTickers`, weight 15. **VERIFIED**
- Response `data.time` (ms snapshot) + `data.ticker[]`: `symbol`, `buy` (best bid), `sell` (best ask),
  `bestBidSize`, `bestAskSize`, `last`, `takerFeeRate`, `makerFeeRate`, `takerCoefficient`. **VERIFIED**
- Source: https://www.kucoin.com/docs-new/rest/spot-trading/market-data/get-all-tickers
- Live 2026-08-27: 1006 tickers; `buy`/`sell` are strings; the symbol id matches `/api/v2/symbols`.

## 2. Spot instruments / status
- `GET /api/v2/symbols`, weight 4: `symbol`, `baseCurrency`, `quoteCurrency`, `enableTrading`,
  `priceIncrement` (tick), `baseIncrement` (step), `minFunds` (minimum order value), `quoteMinSize`. **VERIFIED**
- Source: https://www.kucoin.com/docs-new/rest/spot-trading/market-data/get-all-symbols
- Live: 1006 symbols, all `enableTrading: true` (no non-tradable row exists to record).

## 3. USDT-M perpetuals
- Instruments + mark/index/funding in ONE call: `GET https://api-futures.kucoin.com/api/v1/contracts/active`, weight 3:
  `symbol`, `baseCurrency`, `quoteCurrency`, `settleCurrency`, `multiplier` ("number of coins in each lot"),
  `tickSize`, `lotSize`, `status` (Init|Open|BeingSettled|Settled|Paused|Closed|CancelOnly),
  `fundingFeeRate`, `predictedFundingFeeRate`, `nextFundingRateDateTime` (ms, "next funding fee settlement time"),
  `fundingRateGranularity` (ms, "configured funding rate interval"), `markPrice`, `indexPrice`, `isInverse`,
  `takerFeeRate`, `makerFeeRate`, `type`. **VERIFIED**
  Source: https://www.kucoin.com/docs-new/rest/futures-trading/market-data/get-all-symbols
  Live: 674 contracts, 664 settle USDT; granularity 1h (2) / 4h (416) / 8h (255) / null (1 delivery contract, `type` FFICSX);
  numbers are BARE JSON numbers (parsed from literal text, never float64). `baseCurrency` for Bitcoin is **XBT** (doc example
  XBTUSDTM) while spot lists **BTC** — the collector applies that single cited alias, nothing else.
- Best bid/ask: `GET /api/v1/allTickers`, weight 5: `symbol`, `bestBidPrice`, `bestBidSize`, `bestAskPrice`, `bestAskSize`, `ts` (ns). **VERIFIED**
  Source: https://www.kucoin.com/docs-new/rest/futures-trading/market-data/get-all-tickers
- Current funding (per symbol, not needed — bulk above): `GET /api/v1/funding-rate/{symbol}/current`, weight 2, public:
  `granularity`, `timePoint`, `value`, `predictedValue`, `fundingTime`. **VERIFIED**
  Source: https://www.kucoin.com/docs-new/rest/futures-trading/funding-fees/get-current-funding-rate
- Funding history: `GET /api/v1/contract/funding-rates?symbol&from&to`, weight 5, public → `fundingRate`, `timepoint`. **VERIFIED**
  Source: https://www.kucoin.com/docs-new/rest/futures-trading/funding-fees/get-public-funding-history
- Interval: `fundingRateGranularity` / 3 600 000 → 1 / 4 / 8 h. **VERIFIED**

## 4. Currency / chain status — PUBLIC
- `GET /api/v3/currencies`, weight 3, no auth: `currency`, `chains[] {chainName, isDepositEnabled, isWithdrawEnabled, …}`. **VERIFIED**
- Source: https://www.kucoin.com/docs-new/rest/spot-trading/market-data/get-all-currencies

## 5. Rate limits / bans
- Public pool **2000 weight / 30 s per IP**, Spot and Futures each (all VIP levels); excess → **HTTP 429, code 429000**;
  headers `gw-ratelimit-limit / -remaining / -reset (ms)`. No IP-ban wording. **VERIFIED**
- Source: https://www.kucoin.com/docs-new/rate-limit-rule-classic.md
- Collector gates: 600 weight / 30 s per host; per poll spot 15, futures 3 + 5; refresh 4 + 3.

## 6. Fees (regular tier)
- Spot taker **0.10 %**: `takerFeeRate: "0.001"` on the BTC-USDT allTickers row (`takerCoefficient` 1.00), field documented as
  "Fee rate for market takers" (§1 source). **VERIFIED (official API response)**
- Futures taker **0.06 %** / maker 0.02 %: `takerFeeRate: 0.0006` on the XBTUSDTM contracts/active row (§3 source) and LV0
  "0.020 % / 0.060 %" on https://www.kucoin.com/announcement/en-futures-fee. **VERIFIED**
- Note: the fee-schedule web page (https://www.kucoin.com/vip/privilege) did not render in a fetch; the API fields are primary.

## 7. Notes
- Envelope `{"code":"200000","data":…}`; anything else is an error.
- Connector complexity: LOW (everything bulk; second venue after Gate with public network status).
