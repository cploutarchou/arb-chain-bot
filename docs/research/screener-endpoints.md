# Cross-Exchange Spot Screener + Perp Basis/Funding Monitor — Public Endpoint Research

Scope: PUBLIC (no API key) endpoints only, spot + USDT-margined perpetuals.
Access date for all citations below: **2026-08-27**, verified against official docs/repos on that date unless otherwise noted.

Legend:
- **VERIFIED** — confirmed present, with field names, from an official source.
- **VERIFIED-ABSENT** — official source confirms the field/endpoint does NOT exist or is not included in that response.
- **UNVERIFIED** — could not confirm from an official/primary source; do not build on it without re-checking.

---

## 1. Binance

### 1.1 Spot bulk ticker (best bid/ask)
- **Endpoint:** `GET /api/v3/ticker/bookTicker` (base `https://api.binance.com`), no `symbol`/`symbols` param → array for **all** symbols. **VERIFIED**
- **Fields:** `symbol`, `bidPrice`, `bidQty`, `askPrice`, `askQty`. **VERIFIED**
- **VERIFIED-ABSENT:** no timestamp field in bookTicker (use receive time).
- **Weight:** 4 (all symbols), IP-based. **VERIFIED**
- Symbol format: concatenated `BTCUSDT` — split base/quote via `exchangeInfo` `baseAsset`/`quoteAsset` (never string-parse). **VERIFIED**

Sources: https://developers.binance.com/docs/binance-spot-api-docs/rest-api/market-data-endpoints , https://raw.githubusercontent.com/binance/binance-spot-api-docs/master/rest-api.md

### 1.2 Spot exchange info
- `GET /api/v3/exchangeInfo`, weight 20. **VERIFIED**
- `status`: `TRADING`, `HALT`, `BREAK`. **VERIFIED**
- Filters: `PRICE_FILTER.tickSize`, `LOT_SIZE.stepSize`/`minQty`, `NOTIONAL` (`minNotional`, `applyMinToMarket`, `maxNotional`, …) replaces legacy `MIN_NOTIONAL`. **VERIFIED**

### 1.3 USDT-M perpetual bulk ticker / mark price / funding
- Book ticker (all): `GET /fapi/v1/ticker/bookTicker` → `symbol`, `bidPrice`, `bidQty`, `askPrice`, `askQty`, `time`. **VERIFIED**
- Mark/index/funding (all): `GET /fapi/v1/premiumIndex`, weight **10** without symbol. Fields: `symbol`, `markPrice`, `indexPrice`, `estimatedSettlePrice`, `lastFundingRate` (currently-accruing/predicted rate for the open interval), `interestRate`, `nextFundingTime`, `time`. **VERIFIED**
- Settled funding history: `GET /fapi/v1/fundingRate` (`symbol` optional; shares a 500/5min/IP budget with `fundingInfo`) → `symbol`, `fundingRate`, `fundingTime`, `markPrice`. **VERIFIED**
- Funding interval: `GET /fapi/v1/fundingInfo` (bulk) lists `symbol`, `adjustedFundingRateCap`, `adjustedFundingRateFloor`, `fundingIntervalHours` **only for symbols adjusted away from the 8 h default**; absent symbols = 8 h. **VERIFIED**
- Contract multiplier: none (1 contract = 1 base unit).

Sources: https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/Mark-Price , …/Get-Funding-Rate-History , …/Get-Funding-Rate-Info , …/Symbol-Order-Book-Ticker , …/Exchange-Information

### 1.4 Currency/chain status
- `GET /sapi/v1/capital/config/getall` — **REQUIRES API KEY** (signed). No public equivalent. **VERIFIED** (https://developers.binance.com/docs/wallet/capital)

### 1.5 Rate limits / bans
- Spot IP weight **6,000/min** (raised from 1,200 on 2023-08-25; confirm live from `exchangeInfo.rateLimits` before hardcoding — MEDIUM confidence). Futures `REQUEST_WEIGHT` **2,400/min**/IP. **VERIFIED**
- 418 = auto IP ban after continued 429s, 2 min → 3 days, `Retry-After` header. **VERIFIED** (https://developers.binance.com/docs/binance-spot-api-docs/rest-api/limits)

### 1.6 Fees
- Spot regular: maker 0.100 % / taker 0.100 %. **VERIFIED** (https://www.binance.com/en/fee/schedule)
- USDⓈ-M futures regular: maker 0.0200 % / taker 0.0500 %. **VERIFIED** (https://www.binance.com/en/fee/futureFee)

### 1.7 Testnet
- Spot testnet `https://testnet.binance.vision/api`. **VERIFIED**

**Connector complexity: LOW.**

---

## 2. OKX

### 2.1 Spot bulk ticker
- `GET /api/v5/market/tickers?instType=SPOT` → all spot instruments. **VERIFIED**
- Fields: `instId`, `bidPx`, `bidSz`, `askPx`, `askSz`, `last`, `ts`. **VERIFIED**
- Rate limit: 20 req/2 s (IP). **VERIFIED**
- Symbol `BTC-USDT`; base/quote also on instruments (`baseCcy`/`quoteCcy`). **VERIFIED**
Source: https://www.okx.com/docs-v5/en/#public-data-rest-api-get-tickers

### 2.2 Instruments
- `GET /api/v5/public/instruments?instType=SPOT|SWAP` (public; do not confuse with authenticated `/api/v5/account/instruments`). **VERIFIED**
- `state`: `live`, `suspend`, `rebase`, `post_only`, `preopen`, `test`, `settling`. **VERIFIED**
- `tickSz`, `lotSz`, `minSz`; SWAP: `ctVal`, `ctValCcy`, `ctType` (`linear`/`inverse`), `settleCcy`. **VERIFIED**
Source: https://www.okx.com/docs-v5/en/#public-data-rest-api-get-instruments

### 2.3 Perps / funding
- `GET /api/v5/market/tickers?instType=SWAP` → last/bid/ask for all perps; **mark price is separate**: `GET /api/v5/public/mark-price?instType=SWAP`. **VERIFIED-ABSENT** (markPrice not in tickers).
- Current/predicted funding: `GET /api/v5/public/funding-rate?instId=…` — **per instrument only**. Fields `fundingRate`, `nextFundingRate`, `fundingTime`, `nextFundingTime`, `instId`. **VERIFIED**
- History: `GET /api/v5/public/funding-rate-history` — per instrument. **VERIFIED**
- Funding interval: no dedicated field confirmed; derive `nextFundingTime − fundingTime` per instrument (OKX runs 8h/4h/2h/1h per instrument). **VERIFIED mechanism, UNVERIFIED field.**
Sources: https://www.okx.com/docs-v5/en/#public-data-rest-api-get-funding-rate , https://www.okx.com/en-us/help/okx-to-adjust-funding-rate-interval-for-certain-perpetual-futures

### 2.4 Currency/chain status
- `GET /api/v5/asset/currencies` **REQUIRES API KEY**. **VERIFIED**

### 2.5 Rate limits
- Tickers 20 req/2 s **VERIFIED**; other paths: check the per-endpoint table before use.

### 2.6 Fees
- Lv1 spot maker 0.08 % / taker 0.10 % reported by aggregators citing OKX FAQ; official fee page did not render; rates otherwise only via authenticated `GET /api/v5/account/trade-fee`. **UNVERIFIED from primary source (MEDIUM).**

### 2.7 Demo
- Same endpoints with header `x-simulated-trading: 1` and a demo API key. **VERIFIED**

**Connector complexity: MEDIUM** (per-instrument funding calls, separate mark price).

---

## 3. Bybit

### 3.1 Spot bulk ticker
- `GET /v5/market/tickers?category=spot` → all. Fields `symbol`, `bid1Price`, `bid1Size`, `ask1Price`, `ask1Size`, `lastPrice`; top-level `time`. **VERIFIED** (per-symbol timestamp UNVERIFIED)
- Symbol `BTCUSDT`; split via instruments-info `baseCoin`/`quoteCoin`. **VERIFIED**
Source: https://bybit-exchange.github.io/docs/v5/market/tickers

### 3.2 Instruments
- `GET /v5/market/instruments-info?category=spot|linear`. `status`: `Trading`, `PreLaunch`, `Delivering` (enum may be longer — re-check). `priceFilter.tickSize`, `lotSizeFilter.basePrecision`/`minOrderQty`/`minOrderAmt` (spot); linear `lotSizeFilter.qtyStep`, `minNotionalValue`. **VERIFIED**
- **`fundingInterval` (minutes) on linear instruments.** **VERIFIED**
Source: https://bybit-exchange.github.io/docs/v5/market/instrument

### 3.3 Perps / funding
- `GET /v5/market/tickers?category=linear` (bulk) → `markPrice`, `indexPrice`, `fundingRate`, `nextFundingTime` for all perps. **VERIFIED**
- History: `GET /v5/market/funding/history?symbol=…` per symbol, max 200. **VERIFIED**
Source: https://bybit-exchange.github.io/docs/v5/market/history-fund-rate

### 3.4 Currency/chain status
- `GET /v5/asset/coin/query-info` **REQUIRES API KEY**. **VERIFIED**

### 3.5 Rate limits
- Public: **600 req / 5 s per IP**; excess → 403 "access too frequent", ~10 min cooldown. **VERIFIED via indexed docs — re-fetch https://bybit-exchange.github.io/docs/v5/rate-limit before hardcoding.**

### 3.6 Fees
- Regular: spot 0.1 %/0.1 %, USDT perp maker 0.02 % / taker 0.055 %. **UNVERIFIED from primary (fee page timed out), MEDIUM.**

### 3.7 Testnet / demo
- `api-testnet.bybit.com`; demo `api-demo.bybit.com`. **VERIFIED** (https://bybit-exchange.github.io/docs/v5/demo)

**Connector complexity: LOW.**

---

## 4. Bitget

Caveat: `bitget.com/api-doc/*` is a client-rendered SPA; all facts came from indexed snippets of the official pages and official-derived SDKs. Treat as **VERIFIED-via-secondary-index**; re-render before hardcoding.

### 4.1 Spot bulk ticker
- `GET /api/v2/spot/market/tickers` (all). Fields `symbol`, `lastPr`, `bidPr`, `bidSz`, `askPr`, `askSz`, `open24h`, `high24h`, `low24h`, `baseVolume`, `quoteVolume`, `ts`. Split via `/api/v2/spot/public/symbols` `baseCoin`/`quoteCoin`.

### 4.2 Spot instruments
- `GET /api/v2/spot/public/symbols`; `status` seen `"online"` (full enum UNVERIFIED); `pricePrecision`, `minTradeAmount`; quantity precision field name UNVERIFIED.

### 4.3 Perps / funding
- `GET /api/v2/mix/market/tickers?productType=USDT-FUTURES` (bulk) incl. `bid1Price/Size`, `ask1Price/Size`, `indexPrice`, `markPrice`, `fundingRate`, `openInterest`.
- `GET /api/v2/mix/market/current-fund-rate` per symbol → `fundingRate`, `fundingRateInterval`, `minFundingRate`, `maxFundingRate`, `nextUpdate`.
- History `GET /api/v2/mix/market/history-fund-rate` per symbol. Contract multiplier field on `/api/v2/mix/market/contracts` — name UNVERIFIED.

### 4.4 Currency/chain status — UNVERIFIED (treat as key-required).
### 4.5 Rate limits — UNVERIFIED (portal not renderable).
### 4.6 Fees — spot 0.1 %/0.1 %, USDT-M maker 0.02 % / taker 0.06 % — UNVERIFIED from primary.
### 4.7 Demo trading API exists; host UNVERIFIED.

Sources: https://www.bitget.com/api-doc/spot/market/Get-Tickers , …/spot/market/Get-Symbols , …/contract/market/Tickers , …/contract/market/Get-Current-Funding-Rate , …/contract/market/Get-All-Symbols-Contracts

**Connector complexity: MEDIUM-HIGH (verification gap).**

---

## 5. Gate (APIv4)

Docs site returned 403 to direct fetch; facts verified against the official auto-generated SDK docs `github.com/gateio/gateapi-python` (same OpenAPI source).

### 5.1 Spot bulk ticker
- `GET /spot/tickers` (all). Fields `currency_pair`, `last`, `lowest_ask`, `highest_bid`, `change_percentage`, `base_volume`, `quote_volume`, `high_24h`, `low_24h`. **VERIFIED**
- **VERIFIED-ABSENT:** `lowest_size`/`highest_size` are "not available for batch queries" — bulk spot has NO bid/ask sizes; no timestamp.
- Symbol `BTC_USDT`; split via `/spot/currency_pairs` `base`/`quote`. **VERIFIED**

### 5.2 Spot instruments
- `GET /spot/currency_pairs`; `trade_status`: `untradable`, `buyable`, `sellable`, `tradable`; `precision`, `amount_precision`, `min_base_amount`, `min_quote_amount`, `max_*`. **VERIFIED**

### 5.3 Perps / funding
- `GET /futures/usdt/tickers` (all) → `last`, `mark_price`, `index_price`, `funding_rate`, `funding_rate_indicative` (predicted next), `lowest_ask`/`lowest_size`, `highest_bid`/`highest_size` (sizes present here). **VERIFIED**
- `GET /futures/usdt/contracts` → `funding_interval` (seconds), `quanto_multiplier`. **VERIFIED**
- History `GET /futures/usdt/funding_rate?contract=…` per contract (required). **VERIFIED**

### 5.4 Currency/chain status
- `GET /spot/currencies` — **PUBLIC, no auth**; per-currency `chains[]` with deposit/withdraw status (top-level `deposit_disabled`/`withdraw_disabled` deprecated in favour of chains). **VERIFIED**

### 5.5 Rate limits — conflicting figures (900 req/s spot public; 300 req/s futures public; 200 req/10 s per endpoint/IP elsewhere). **UNVERIFIED — reconcile.**
### 5.6 Fees — VIP0 spot 0.20 %/0.20 %, futures 0.015 %/0.05 %, structure changed 2026-04-09. **UNVERIFIED from primary.**
### 5.7 Testnet — futures `https://fx-api-testnet.gateio.ws/api/v4`. **VERIFIED**

Sources: https://github.com/gateio/gateapi-python/blob/master/docs/SpotApi.md , …/Ticker.md , …/CurrencyPair.md , …/FuturesApi.md , …/FuturesTicker.md , …/Contract.md , …/Currency.md

**Connector complexity: LOW-MEDIUM** (no spot sizes in bulk).

---

## 6. MEXC

### 6.1 Spot bulk ticker
- `GET /api/v3/ticker/bookTicker` (all), weight 1. Fields `symbol`, `bidPrice`, `bidQty`, `askPrice`, `askQty`. **VERIFIED** (https://mexcdevelop.github.io/apidocs/spot_v3_en/#order-book-ticker)

### 6.2 Spot exchange info
- `GET /api/v3/exchangeInfo`; `status` numeric: `1` online, `2` pause, `3` offline; `isSpotTradingAllowed`; flat fields `baseAssetPrecision`, `quoteAssetPrecision`, `baseSizePrecision`, `quoteAmountPrecision`, `maxQuoteAmount` (no `filters[]`). **VERIFIED**

### 6.3 Perps / funding (base `https://contract.mexc.com`)
- `GET /api/v1/contract/ticker` (all), 20 req/2 s → `fundingRate`, `lastPrice`, `bid1`, `ask1`, `holdVol`, `timestamp`; mark/index location **UNVERIFIED** (possibly `/api/v1/contract/fair_price`, `/index_price`).
- `GET /api/v1/contract/funding_rate/{symbol}` → `fundingRate`, `maxFundingRate`, `minFundingRate`, `collectCycle` (hours), `nextSettleTime`. **VERIFIED**
- History `GET /api/v1/contract/funding_rate/history` per symbol (paged). **VERIFIED**
- `GET /api/v1/contract/detail` — **1 req / 5 s** — `contractSize`, `priceUnit`, `volUnit`, `state` (`0` enabled … `4` paused), `minVol`/`maxVol`, `apiAllowed`. **VERIFIED**

### 6.4 Currency/chain status
- `GET /api/v3/capital/config/getall` **REQUIRES API KEY**. **VERIFIED**

### 6.5 Rate limits
- Spot **500 req / 10 s per endpoint per IP**; 418/429 language mirrors Binance. **VERIFIED (docs text)**
- Contract API answers over-frequency **in-band**: HTTP 200 with error code **510** ("Excessive frequency of requests" in the error-code table, https://mexcdevelop.github.io/apidocs/contract_v1_en/#error-code, accessed 2026-08-27; observed body `{"code":510,"msg":"Requests are too frequent"}`). No Retry-After is published. **VERIFIED (code + description); the pause length is our policy** — `internal/screener/venue/mexc.go` counts it as rate_limited and pauses the venue gate 10 s.

### 6.6 Fees — spot 0 % maker / 0.05 % taker (promos vary), USDT-M 0 % / 0.02 %. **UNVERIFIED from primary; promos change often.**
### 6.7 Testnet — none documented at API level. **UNVERIFIED.**

**Connector complexity: MEDIUM.**

---

## 7. Comparison

| Venue | Bulk spot bid/ask (+size?) | Bulk perp w/ mark+funding | Funding interval | Funding history | Public currency/chain | Confidence |
|---|---|---|---|---|---|---|
| Binance | yes, +size, no ts | yes (`premiumIndex`) | `fundingInfo` bulk (adjusted only; default 8 h) | per symbol | no (key) | HIGH |
| OKX | yes, +size +ts | partial (mark separate) | derive from timestamps | per instrument | no (key) | MEDIUM |
| Bybit | yes, +size | yes (bulk) | `fundingInterval` min on instruments | per symbol | no (key) | HIGH |
| Bitget | yes (+size, +ts) unverified | yes unverified | `fundingRateInterval` per symbol | per symbol | UNVERIFIED | MEDIUM |
| Gate | price only (**no sizes**) | yes, incl. predicted + sizes | `funding_interval` s on contracts | per contract | **yes, public** | HIGH (SDK docs) |
| MEXC | yes, +size | funding yes; mark/index UNVERIFIED | `collectCycle` h per symbol | per symbol | no (key) | MEDIUM-HIGH |

Design consequences: Gate spot rows carry `liquidity: unknown` (no sizes in bulk); OKX and Bitget/MEXC funding need per-symbol calls (paced, low cadence); currency/chain status is `unknown (venue requires API key)` everywhere except Gate; fee defaults for OKX/Bybit/Bitget/Gate/MEXC are marked unverified in the settings UI until re-checked against a rendered fee page.

---

## 8. Re-verification log — 2026-08-27 (T-066 implementation)

Facts re-checked against official sources while building `internal/screener/venue/`; each supersedes the entry above where they differ.

- **Bitget docs moved.** `bitget.com/api-doc/spot/*` and `/contract/*` now redirect to the UTA (v3) intro; the v2 pages render under `/api-doc/classic/…`. Verified there (VERIFIED, 2026-08-27):
  - `GET /api/v2/spot/market/tickers` — 20 req/1 s (IP); `symbol, lastPr, bidPr, askPr, bidSz, askSz, ts` (https://www.bitget.com/api-doc/classic/spot/market/Get-Tickers)
  - `GET /api/v2/spot/public/symbols` — 20 req/1 s; `status` enum `offline|gray|online|halt`; `pricePrecision`, `quantityPrecision` (decimal places), `minTradeUSDT` (https://www.bitget.com/api-doc/classic/spot/market/Get-Symbols)
  - `GET /api/v2/mix/market/tickers?productType=USDT-FUTURES` — 20 req/1 s; `bidPr, askPr, bidSz, askSz, indexPrice, markPrice, fundingRate, ts` (https://www.bitget.com/api-doc/classic/contract/market/Get-All-Symbol-Ticker)
  - `GET /api/v2/mix/market/contracts` — 20 req/s; `symbolStatus` enum `listed|normal|maintain|limit_open|restrictedAPI|off`; `pricePlace`, `volumePlace`, `sizeMultiplier`, `minTradeNum`, `minTradeUSDT`, `fundInterval` (hours), `symbolType perpetual|delivery` (https://www.bitget.com/api-doc/classic/contract/market/Get-All-Symbols-Contracts)
  - `GET /api/v2/mix/market/current-fund-rate` — 20 req/1 s, per symbol; `fundingRate, fundingRateInterval (1|2|4|8 h), nextUpdate (ms), min/maxFundingRate` (https://www.bitget.com/api-doc/classic/contract/market/Get-Current-Funding-Rate)
  - Still UNVERIFIED: ban status code / cool-down (classic rate-limit page 404s); chain status; fees. Collector uses a 10 req/s venue gate.
- **MEXC contract bulk ticker mark/index: VERIFIED.** `GET /api/v1/contract/ticker` (20 req/2 s) returns `indexPrice`, `fairPrice` (mark), `fundingRate`, `bid1`, `ask1`, `timestamp` as bare JSON numbers (https://mexcdevelop.github.io/apidocs/contract_v1_en/). `funding_rate/{symbol}`: `collectCycle` (h), `nextSettleTime` (ms). `contract/detail` 1 req/5 s: `baseCoin, quoteCoin, settleCoin, contractSize, priceUnit, volUnit, state, apiAllowed`.
- **OKX per-endpoint limits (VERIFIED, https://www.okx.com/docs-v5/en/):** tickers 20/2 s, instruments 20/2 s, mark-price 10/2 s, funding-rate 10/2 s, all per IP. `mark-price` fields `instId, markPx, ts`. Live SWAP instruments carry `baseCcy=""`; base/quote for linear swaps come from `ctValCcy`/`settleCcy`.
- **Bybit (VERIFIED, https://bybit-exchange.github.io/docs/v5/rate-limit):** 600 req / 5 s per IP; excess → HTTP 403 "access too frequent", ≥10 min ban. Spot bulk ticker has no per-symbol timestamp (receive time used).
- **Binance:** `exchangeInfo.rateLimits` live on 2026-08-27: spot REQUEST_WEIGHT 6000/min, futures 2400/min. Retry-After on 429/418 is seconds (https://developers.binance.com/docs/binance-spot-api-docs/rest-api/limits).
- **Gate (SDK docs, VERIFIED):** `Contract.funding_next_apply` (unix s, float) and `status prelaunch|trading|delisting|delisted|circuit_breaker` (…/docs/Contract.md); `SpotCurrencyChain {name, addr, withdraw_disabled, withdraw_delayed, deposit_disabled}` (…/docs/SpotCurrencyChain.md); batch `/spot/tickers` may return `""` for `lowest_ask`/`highest_bid` (observed live). `Contract` has no base/quote fields — resolved by looking the contract name up in `/spot/currency_pairs.id`. Rate limits remain UNVERIFIED (docs 403 to non-browser fetch); collector uses 10 req/s.
- **Fees:** only Binance verified from its fee pages; OKX/Bybit/Bitget/Gate/MEXC stay UNVERIFIED and are flagged `Verified=false` in `venue.Registry()`.
