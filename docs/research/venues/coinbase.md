# Coinbase (Advanced Trade public market data) — research (T-075)

Scope: PUBLIC endpoints only; Coinbase Advanced Trade (api.coinbase.com) spot. Access date for every citation: **2026-08-27**.
Legend as in docs/research/screener-endpoints.md. Collector: internal/screener/venue/coinbase.go. Fixtures: internal/screener/testdata/coinbase/.

## 1. Spot bulk ticker — VERIFIED-ABSENT
- `GET /api/v3/brokerage/market/products` (public, "security: []") lists products with `price`, `volume_24h`, `best_bid_price`,
  `best_ask_price`, … **VERIFIED schema**, but live the two best-* fields are EMPTY STRINGS on every one of the 929 public rows and
  `GET /api/v3/brokerage/best_bid_ask` is private. **There is no public bulk bid/ask on this venue.**
- Per product: `GET /api/v3/brokerage/market/product_book?product_id=BTC-USD&limit=1` → `pricebook {product_id, bids[] {price, size},
  asks[] {price, size}, time (RFC 3339)}`, `last`, `mid_market`, `spread_bps`. Listed under the public endpoints of the REST index
  and answered without a key live (12 consecutive 200s). **VERIFIED**
  Sources: https://docs.cdp.coinbase.com/api-reference/advanced-trade-api/rest-api/public/list-public-products ,
  https://docs.cdp.coinbase.com/api-reference/advanced-trade-api/rest-api/public/get-public-product-book ,
  https://docs.cdp.coinbase.com/coinbase-app/advanced-trade-apis/rest-api.md
- Collector: Spot() fetches `books_per_poll` (default 40) products round-robin per poll and returns every product's last-known quote
  with its own `pricebook.time`, so ages are honest; a full sweep of ~920 products takes ~23 polls.

## 2. Spot instruments / status
- Same `market/products` call: `product_id`, `base_currency_id`, `quote_currency_id`, `status` (online|delisted live), `trading_disabled`,
  `is_disabled`, `view_only`, `cancel_only`, `limit_only`, `post_only`, `auction_mode`, `price_increment` (tick), `base_increment` (step),
  `quote_min_size` (min notional), `product_type` (SPOT|FUTURE|…), pagination cursor (single page live). **VERIFIED**
- Live: 929 products (921 online, 8 delisted); quotes USD 405, USDC 409, EUR 34, GBP 23, BTC 24, USDT 21, others.

## 3. Perpetuals — NONE on Advanced Trade public market data
- All 929 public products are `product_type: SPOT`. Coinbase perpetual futures are an international offering (Coinbase International
  Exchange, moving to a Deribit-powered gateway with a hard cutover on 2026-09-09) and US CFTC futures via an FCM — neither appears in
  Advanced Trade public market data for retail. Perps() returns an empty slice. **VERIFIED-ABSENT**
- Source: https://docs.cdp.coinbase.com/coinbase-app/advanced-trade-apis/guides/derivatives/overview.md

## 4. Currency / network status — PUBLIC (Coinbase Exchange API)
- Advanced Trade has no currency-status endpoint. The sibling Coinbase Exchange public `GET https://api.exchange.coinbase.com/currencies`
  → `id`, `status` online|delisted, `supported_networks[] {id, name, status, …}`. This is NETWORK status, not deposit/withdraw flags —
  Networks() reports open when the currency and ≥ 1 network are online, closed otherwise, and documents that caveat. **VERIFIED (live)**
- Source: https://docs.cdp.coinbase.com/exchange/rest-api/api-reference.md

## 5. Rate limits
- Advanced Trade public endpoints: "throttled by IP at 10 requests per second" — the page
  (https://docs.cdp.coinbase.com/advanced-trade/docs/rest-api-rate-limits) did not render in a fetch; figure from its search excerpt.
  The architecture page states 10,000 req/h per key/OAuth user and HTTP 429 "Too many requests". **UNVERIFIED for the per-IP figure**
- Coinbase Exchange public: "Requests per second per IP: 10", "in bursts: Up to 15". **VERIFIED**
  https://docs.cdp.coinbase.com/exchange/introduction/rate-limits-overview.md
- Collector gate: one 8 req/s gate across both hosts (books dominate: 40 per poll ≈ 5 s).

## 6. Fees — UNVERIFIED
- Official fee pages (https://www.coinbase.com/advanced-fees , https://help.coinbase.com/en/coinbase/trading-and-funding/advanced-trade/advanced-trade-fees)
  answer 403 to fetches. Placeholder: Advanced "Intro" tier 0.60 % maker / **1.20 % taker**. **UNVERIFIED**
- The screener settings cap fees at 100 bps (settings.go `maxFeeBps`), so `Settings.Defaults()` carries a CAPPED 100 bps for Coinbase
  until the cap is raised; `Fees()` on the collector reports the 120 bps placeholder, flagged unverified.

## 7. Notes
- Products response is a plain object `{"products":[…],"num_products":N,"pagination":{…}}`; product_book is `{"pricebook":{…}}`.
- Connector complexity: MEDIUM (per-product books, two hosts).
