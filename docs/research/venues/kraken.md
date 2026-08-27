# Kraken — public endpoint research (T-075)

Scope: PUBLIC endpoints only; Kraken Pro spot (api.kraken.com) + Kraken Futures perpetuals (futures.kraken.com).
Access date for every citation: **2026-08-27**. Legend as in docs/research/screener-endpoints.md.
Collector: internal/screener/venue/kraken.go. Fixtures: internal/screener/testdata/kraken/.

## 1. Spot bulk ticker
- `GET /0/public/Ticker` with `pair` omitted → "all tradeable assets"; result keyed by pair; `a` = Ask `[price, whole lot volume,
  lot volume]`, `b` = Bid `[same]`, `c` last, … **VERIFIED**
- No timestamp field. **VERIFIED-ABSENT** (receive time used).
- `assetVersion` "controls whether response keys and asset identifier fields use Kraken's internal names or display names";
  `assetVersion=1` → keys like `BTC/USD` and `base: "BTC"` instead of `XXBTZUSD` / `XXBT`. The collector uses it for Ticker and
  AssetPairs, so no X/Z-prefix or XBT/XDG alias table exists in code. **VERIFIED**
- Source: https://docs.kraken.com/api/docs/rest-api/get-ticker-information
- Live: 1466 keys (29 `…:BTNL` keys have no AssetPairs row and are ignored); 1377 of 1383 online pairs have a ticker.

## 2. Spot instruments / status
- `GET /0/public/AssetPairs?assetVersion=1` → `altname`, `wsname`, `base`, `quote`, `status` (online|cancel_only|post_only|limit_only|
  reduce_only), `tick_size`, `lot_decimals` (volume decimals → step 10^-n), `pair_decimals`, `ordermin`, `costmin` (min cost in quote). **VERIFIED**
- Source: https://docs.kraken.com/api/docs/rest-api/get-tradable-asset-pairs
- Live: 1437 pairs — 1383 online, 37 cancel_only, 17 post_only; quotes mostly USD (638) / EUR (519); 44 USDT, 44 USDC pairs.

## 3. Perpetuals (Kraken Futures)
- There is **no USDT-margined perp**: PF_* "flexible" perpetuals are USD-quoted, multi-collateral ("cash settled in USD"). Quote = "USD". **VERIFIED**
- Instruments: `GET /derivatives/api/v3/instruments` (public) → `symbol`, `type` flexible_futures|futures_inverse|…, `tradeable`,
  `tickSize`, `contractSize`, `contractValueTradePrecision`, `base` ("BTC"), `quote` ("USD"), `pair`. **VERIFIED**
  Source: https://docs.kraken.com/api/docs/futures-api/trading/get-instruments
  The perpetual/dated distinction is NOT on this list — it is the tickers' `tag` (perpetual|month|quarter|semiannual|week). **VERIFIED (live)**
- Tickers (bulk): `GET /derivatives/api/v3/tickers` (public) → `symbol`, `pair`, `tag`, `bid`, `bidSize`, `ask`, `askSize`, `markPrice`,
  `indexPrice`, `fundingRate` ("current absolute funding rate"), `fundingRatePrediction` ("estimated next absolute funding rate"),
  `suspended`, `postOnly`, `last`, `lastTime`; `serverTime`. Bare JSON numbers. **VERIFIED**
  Source: https://docs.kraken.com/api/docs/futures-api/trading/get-tickers — live: 298 tickers, 280 perpetual, none with null mark/bid/funding.
- Funding period and units: "settlement every 1 hour"; absolute rate = "funding an account will receive by maintaining a 1 contract
  unit short position for 1 hour"; relative rate = absolute "relative to the spot price at the time of funding rate calculation";
  cap ±0.5 % per hour. **VERIFIED**
  Source: https://support.kraken.com/articles/4844359082772-linear-multi-collateral-derivatives-contract-specifications
- History (per symbol): `GET /derivatives/api/v4/historicalfundingrates?symbol=PF_XBTUSD` → `rates[] {timestamp (hourly), fundingRate,
  relativeFundingRate}`; the last row is the current hour. **VERIFIED (live)**; the reference page 404s in fetch.
- Collector: FundingRate = exact `relativeFundingRate` (round-robin, `funding_calls_per_poll` symbols per poll, carried while < 1 h old),
  else absolute ÷ indexPrice; PredictedFundingRate = fundingRatePrediction ÷ indexPrice; IntervalH = 1; NextFundingAt = next whole hour.

## 4. Currency / chain status
- No public endpoint: DepositMethods / DepositStatus are private. → "unknown (venue requires API key)". **VERIFIED**
  https://docs.kraken.com/api/docs/rest-api/get-deposit-methods

## 5. Rate limits / bans
- Spot public: "Calling the public endpoints at a frequency of 1 per second (or less) would remain within the rate limits"; excess is
  restricted "for a few seconds (or possibly longer if calls continue)"; per IP. **VERIFIED**
  https://support.kraken.com/hc/en-us/articles/206548367-What-are-the-API-rate-limits — the docs page
  https://docs.kraken.com/api/docs/guides/spot-rest-ratelimits covers authenticated limits only ("EAPI:Rate limit exceeded").
- Futures: "Public endpoints do not have a cost and therefore do not count against any rate limiting budget"; exceeding → `apiLimitExceeded`. **VERIFIED**
  https://docs.kraken.com/api/docs/guides/futures-rate-limits
- Collector gates: spot 1 req/s, futures 10 req/s.

## 6. Fees — VERIFIED
- Kraken Pro Spot Crypto, Tier 1 "$0+": maker **0.40 %**, taker **0.80 %** (Tier 2 $2.5K+: 0.30/0.60; Tier 3 $10K+: 0.22/0.38).
- Futures Tier 1 "< $5M": maker 0.02 %, taker **0.05 %**.
- Stablecoin/FX pairs Tier 1: 0.20 % / 0.20 % (not modelled; spreads on stable pairs are over-charged by the regular rate).
- Source: https://www.kraken.com/features/fee-schedule

## 7. Notes
- Envelopes: spot `{"error":[],"result":…}`; futures `{"result":"success", …}`.
- Connector complexity: LOW–MEDIUM (two hosts, relative funding via history).
