# Crypto.com Exchange — public endpoint research (T-078)

Scope: PUBLIC (no API key) endpoints only; spot + perpetuals. Access date for every
citation: **2026-08-27**. Legend as in docs/research/screener-endpoints.md
(VERIFIED / VERIFIED-ABSENT / UNVERIFIED).
Collector: internal/screener/venue/cryptocom.go. Fixtures: internal/screener/testdata/cryptocom/.
Doc root: https://exchange-developer.crypto.com/exchange/v1/ (Exchange API v1; every page
below is the site's own `.md` rendering of the same page).

## 1. Spot bulk ticker (bid/ask, NO sizes)
- `GET https://api.crypto.com/exchange/v1/public/get-tickers` (instrument_name omitted → all). **VERIFIED**
- `result.data[]`: `i` instrument name, `b` "current best bid price, null if there aren't any bids",
  `k` "current best ask price, null if there aren't any asks", `a` last, `h`/`l`/`v`/`vv`/`oi`,
  `t` "the published timestamp in ms". **VERIFIED**
- Source: https://exchange-developer.crypto.com/exchange/v1/docs/api/rest/public-get-tickers.md
- **No bid/ask SIZE fields exist** on this endpoint (VERIFIED-ABSENT — the documented field list is
  i/h/l/a/v/vv/c/b/k/oi/t only; live 2026-08-27: 955 rows, no size keys) → quotes carry
  `LiquidityUnknown`, sizes zero, like Gate.

## 2. Instruments / status (spot + perp in one call)
- `GET https://api.crypto.com/exchange/v1/public/get-instruments`: `symbol`, `inst_type`
  (e.g. `PERPETUAL_SWAP`; live values CCY_PAIR | PERPETUAL_SWAP | FUTURE), `base_ccy`, `quote_ccy`,
  `price_tick_size`, `qty_tick_size`, `tradable` (bool), `contract_size`, `expiry_timestamp_ms`,
  `max_leverage`, `margin_buy_enabled`, `margin_sell_enabled`. **VERIFIED**
- Source: https://exchange-developer.crypto.com/exchange/v1/docs/api/rest/public-get-instruments.md
- Live 2026-08-27: 955 instruments (577 CCY_PAIR, 366 PERPETUAL_SWAP, 12 FUTURE), all `tradable: true`.
  Every PERPETUAL_SWAP quotes in **USD** (like Kraken's PF_ contracts — there is no USDT-margined perp);
  the ticker row key is the same `symbol` (e.g. `BTC_USD`, `BTCUSD-PERP`).
- No minimum-notional field is published (VERIFIED-ABSENT) → MinNotional "".

## 3. Perpetuals: mark / index / funding — PER INSTRUMENT
- `GET /public/get-valuations?instrument_name=&valuation_type=&count=`; valuation types (quoted from the doc):
  - `index_price`: "returns per minute data of underlying reference price of the instrument";
  - `mark_price`: "returns per minute data of mark price of the instrument";
  - `funding_hist`: "returns hourly data of the funding rate settled in past hourly settlement";
  - `funding_rate`: "returns per minute data of current hourly funding rate that will settle at the
    end of each hour of current 4-hour interval";
  - `estimated_funding_rate`: "returns per minute data of estimated funding rate for the next interval".
  Response `result.data[] {v, t}` + `instrument_name`. **VERIFIED**
- Source: https://exchange-developer.crypto.com/exchange/v1/docs/api/rest/public-get-valuations.md
- Funding interval: settlement is HOURLY ("settled in past hourly settlement", funding_hist rows are
  1 h apart live) → IntervalH = 1; NextFundingAt = next top of hour. **VERIFIED**
- One instrument per call → the collector round-robins `FundingCallsPerPoll` contracts per Perps()
  poll (mark_price + funding_hist + estimated_funding_rate, 3 calls each) and carries last-known
  values; contracts not yet visited are omitted until their first visit. Bid/ask for every contract
  come from the bulk get-tickers call. Index is not fetched (one more call per contract) → Index 0.
- Funding history (same endpoint, `funding_hist` with start_ts/end_ts, default 30 days back). **VERIFIED**

## 4. Currency / chain status — PRIVATE only
- `private/get-currency-networks` is POST with `api_key` + `sig` (request body shows both; it is in
  the private section). No public equivalent exists. **VERIFIED** →
  Networks answers "unknown (venue requires API key)".
- Source: https://exchange-developer.crypto.com/exchange/v1/docs/api/rest/private-get-currency-networks.md

## 5. Rate limits / bans
- "For public market data calls, rate limits are per API method, per IP address: All —
  100 requests per second each." Exceeding → HTTP 429, code 42901
  "TOO_MANY_REQUESTS:Requests have exceeded rate limits". **VERIFIED**
- Source: https://exchange-developer.crypto.com/exchange/v1/docs/api/rest-common-api-reference.md
- No Retry-After / ban wording documented. Collector gate: 50 requests / 1 s.
- Envelope: `{id, method, code, result}`; `code` 0 = success (same page). **VERIFIED**

## 6. Fees (regular tier) — UNVERIFIED
- The official fee page https://crypto.com/exchange/document/fees-limits renders only via
  JavaScript (fetched 2026-08-27: no fee table in the HTML) and the help-center articles cover the
  App, not the Exchange tier table. No official machine-readable number was obtainable →
  **UNVERIFIED** placeholders: spot taker 50 bps, perp taker 7 bps (settings.go defaultVenueFees,
  flagged unverified; Registry Verified=false).

## 7. Notes
- All prices/rates are decimal strings in the JSON (`b`, `k`, `v` quoted) — parsed from literal text.
- Perp base/quote come from the instrument row (`base_ccy`/`quote_ccy`), never from the symbol string.
- Connector complexity: MEDIUM (bulk tickers, but per-instrument valuations for mark/funding).
