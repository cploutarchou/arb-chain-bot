# Bitfinex — public endpoint research (T-078)

Scope: PUBLIC endpoints only; spot + USDt-margined perpetuals. Access date for every
citation: **2026-08-27**. Legend as in docs/research/screener-endpoints.md.
Collector: internal/screener/venue/bitfinex.go. Fixtures: internal/screener/testdata/bitfinex/.
Docs: https://docs.bitfinex.com (append `.md` to any page for its markdown rendering).

## 1. Spot bulk ticker (bid/ask + documented aggregate sizes)
- `GET https://api-pub.bitfinex.com/v2/tickers?symbols=ALL`. **VERIFIED**
- Trading-pair rows (symbol prefix `t`): `[SYMBOL, BID, BID_SIZE, ASK, ASK_SIZE, CHANGE,
  CHANGE_RELATIVE, LAST_PRICE, VOLUME, HIGH, LOW, FIRST_TRADE]`; BID_SIZE is the
  "Sum of the 25 highest bid sizes", ASK_SIZE the "Sum of the 25 lowest ask sizes" (NOT a
  top-of-book size — documented aggregate; the collector stores it as BidQty/AskQty with this
  caveat cited). Funding rows (prefix `f`) have a different 18-field shape and are skipped. **VERIFIED**
- Source: https://docs.bitfinex.com/reference/rest-public-tickers.md — "Rate Limit: 30 reqs/min".
- No timestamp on the row (VERIFIED-ABSENT) → At = poll time.

## 2. Instruments (Configs) — ONE multi-key call
- `GET https://api-pub.bitfinex.com/v2/conf/{key1},{key2},…` returns one array per key. **VERIFIED**
- Keys used (all on https://docs.bitfinex.com/reference/rest-public-conf.md):
  - `pub:list:pair:exchange` — "Fetch a list of valid exchange trading pairs" (live 197);
  - `pub:list:pair:futures` — "Fetch a list of valid derivative pairs" (live 93, e.g. BTCF0:USTF0);
  - (`pub:info:pair` / `pub:info:pair:futures` exist — per-pair `[PAIR, [.., MIN_ORDER_SIZE(idx3),
    MAX_ORDER_SIZE(idx4), ..]]` — but carry no tick/step/base/quote, so the collector does not
    request them);
  - `pub:map:currency:undl` — "Maps derivatives symbols to their underlying currency
    (e.g. BTCF0 -> BTC)";
  - `pub:map:currency:sym` — "Map symbols to their API symbols (e.g. DSH -> DASH)" (live also
    UST -> USDt, ALG -> ALGO, XAUT -> XAUt); the collector applies this map then uppercases, so
    UST/USTF0 normalise to USDT;
  - `pub:info:tx:status` — "wallet status for each currency for deposits and withdrawals
    (1 = active, 0 = maintenance)": `[METHOD, DEP_STATUS, WD_STATUS, …]`;
  - `pub:map:tx:method` — "Maps currencies to their appropriate method(s) for API withdrawals
    (e.g. UST -> [TETHERUSDTBCH, …])" (used inverted: method → currencies);
  - `pub:list:currency` — "Fetch a list of all currencies available on the platform" (live 255).
- Pair → base/quote: a pair string is `BASE:QUOTE` when it contains a colon, else 3+3 letters
  (doc examples on the Tickers page: "tBTCUSD, tETHUSD, tBTCUST"; conf examples "AAVE:USD").
  The split is validated against `pub:list:currency` — live 2026-08-27 every half of all 197+93
  pairs is a listed currency, and every no-colon pair is exactly 6 chars; a pair whose halves are
  not both listed currencies is dropped. Bitfinex publishes no explicit base/quote field
  (VERIFIED-ABSENT), so this documented-format + currency-list validation is the instrument-derived
  mapping.
- Tradability: a pair's presence in `pub:list:pair:exchange` / `pub:list:pair:futures` (the venue's
  "valid … pairs" lists) is the tradable signal; no per-pair status field exists (VERIFIED-ABSENT).

## 3. Perpetuals (USDt-margined = *F0:USTF0)
- `GET https://api-pub.bitfinex.com/v2/status/deriv?keys=ALL` (rate limit "90 reqs/min"):
  `[KEY, MTS, _, DERIV_PRICE, SPOT_PRICE, _, INSURANCE_FUND_BALANCE, _, NEXT_FUNDING_EVT_MTS,
  NEXT_FUNDING_ACCRUED ("Current accrued funding for next 8h period"), NEXT_FUNDING_STEP, _,
  CURRENT_FUNDING ("Funding applied in the current 8h period"), _, _, MARK_PRICE ("Price based on
  the BFX Composite Index"), _, _, OPEN_INTEREST, _, _, _, CLAMP_MIN, CLAMP_MAX]`. **VERIFIED**
- Source: https://docs.bitfinex.com/reference/rest-public-derivatives-status.md
- Funding interval: the field docs say "8h period" twice → IntervalH = 8; NextFundingAt =
  NEXT_FUNDING_EVT_MTS. FundingRate = CURRENT_FUNDING, PredictedFundingRate =
  NEXT_FUNDING_ACCRUED. **VERIFIED**
- Bid/ask (+ the §1 aggregate sizes) for `t<PAIR>` rows come from the same bulk /v2/tickers call.
- History: `GET /v2/status/deriv/{symbol}/hist` (not polled),
  https://docs.bitfinex.com/reference/rest-public-derivatives-status-history.md ("90 reqs/min"). **VERIFIED**
- Only pairs quoted `USTF0` (USDt-settled) are kept: 93/93 live futures pairs are `*F0:USTF0`.

## 4. Currency / chain status — PUBLIC
- `pub:info:tx:status` (§2) is per deposit/withdraw METHOD; `pub:map:tx:method` maps method →
  currencies. Status open when ≥ 1 method for the currency has DEP_STATUS=1 and WD_STATUS=1;
  closed otherwise; currencies with no method → unknown ("no method published"). **VERIFIED**

## 5. Rate limits / bans
- "The current rate limit is between 10 and 90 requests per minute, depending on the specific REST
  API endpoint"; when limited "the IP is blocked for 60 seconds" and the API returns
  `{"error": "ERR_RATE_LIMIT"}`. Per-page numbers: tickers 30 reqs/min; status/deriv 90 reqs/min.
  **VERIFIED** — https://docs.bitfinex.com/docs/requirements-and-limitations.md
- Collector gate: 15 requests / 60 s (well under the tightest per-endpoint 30/min; the poller
  makes ≤ 2 requests per poll + 1 conf refresh per 10 min).

## 6. Fees (regular tier) — UNVERIFIED
- The official fee page https://www.bitfinex.com/fees on 2026-08-27 lists "Spot and Margin trades —
  Maker fees Zero, Taker Fees Zero" and "Derivatives Trades — Maker fees Zero, Taker Fees Zero"
  (a promotional zero-fee schedule; the site also brands "Zero trading fees"). A zero taker fee is
  not a durable regular-tier number to build spread math on (platform rule: nothing is described
  as guaranteed), so the settings keep **UNVERIFIED** placeholders: spot taker 20 bps, perp taker
  6.5 bps; Registry Verified=false. Revisit when the venue publishes a standard schedule again.

## 7. Notes
- All numbers are BARE JSON floats — parsed from their literal text via decimal, never float64.
- Envelope: raw arrays; errors come as `["error", CODE, "MSG"]` / `{"error": …}` with non-200 status.
- Connector complexity: MEDIUM (everything bulk; positional arrays; one conf call for instruments).
