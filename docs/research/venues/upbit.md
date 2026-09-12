# Upbit — public endpoint research (T-075)

Scope: PUBLIC (no API key) endpoints only. Upbit is a spot-only exchange
(no perpetual futures product exists), so the perp section records the
absence. Access date for every citation: **2026-09-13**. Legend as in
docs/research/screener-endpoints.md (VERIFIED / VERIFIED-ABSENT /
UNVERIFIED). Planned collector: internal/screener/venue/upbit.go.

The official docs host (`api-docs.upbit.com`) does not resolve from this
environment (same egress class as the DEX hosts in T-110); every
endpoint fact below is therefore verified by LIVE call against
`https://api.upbit.com`, and the doc-only items (fees) are UNVERIFIED
rather than guessed.

## 1. Spot bulk ticker / order book (bid/ask + size)
- `GET /v1/market/all` → `[{market, korean_name, english_name}]`, no
  auth. Market id is **quote-first**: `KRW-BTC`, `BTC-ETH`, `USDT-SOL`.
  Live 2026-09-13: **853 markets — 288 KRW-, 328 BTC-, 237
  USDT-quoted**. **VERIFIED**
- `GET /v1/orderbook?markets=KRW-BTC,KRW-ETH` — comma-separated list
  VERIFIED live (multi-market). Response per market:
  `{market, timestamp (ms), total_ask_size, total_bid_size,
  orderbook_units: [{bid_price, bid_size, ask_price, ask_size}, …]}` —
  `orderbook_units[0]` IS the best level with sizes; numbers are bare
  JSON numbers (collector must parse from literal text, never
  float64). **VERIFIED** (live, KRW-BTC and a 2-market call)
- `GET /v1/ticker?markets=KRW-BTC&markets=KRW-ETH` — repeated param
  VERIFIED live; carries `trade_price` and 24 h rolls but **no bid/ask
  and no sizes** — not the collector's source. **VERIFIED**
- `GET /v1/ticker/all` → `400 {"error":{"name":400,"message":"Invalid
  parameter…"}}` both bare and with `?quote=KRW`. Bulk-all ticker is
  not usable. **VERIFIED-ABSENT**
- `KRW-USDT` exists as a market — the venue's own FX rate; noted for
  the KRW lane's context, NOT used to convert quotes (the screener
  pairs same-quote assets only).

## 2. Spot instruments / constraints
- §1 `/v1/market/all` is the instrument list; it carries names but no
  tick/step/min-size fields, and no public endpoint for symbol rules
  was found. **UNVERIFIED** (no constraints source; collector emits
  instruments without venue-side precision rules, like other venues
  that publish none).

## 3. Perpetuals
- Upbit operates spot markets only — the market list has no derivatives
  section and no perp product exists on the venue. **VERIFIED-ABSENT**
  (from the live §1 market list).

## 4. Currency / chain status — PUBLIC
- No public deposit/withdrawal-status endpoint: `/v1/market/warnings`
  → 404 `no Route matched` (live). Deposit/withdrawal status is behind
  the authenticated `/v1/withdraws` / `/v1/deposits` family.
  **VERIFIED-ABSENT**

## 5. Rate limits / bans
- Every quotation response carries `remaining-req:
  group=orderbook; min=600; sec=9` (and `group=market` for
  `/v1/market/all`) — per-group windows of **600 requests/minute and
  10 requests/second**. **VERIFIED (official response header, live)**
- Collector gates (plan): orderbook group 600/min; a full 853-market
  sweep at ≤ 40 markets/request is ~22 requests per poll — well inside
  both windows.

## 6. Fees (regular tier)
- KRW market **0.05 % maker / 0.05 % taker**; BTC and USDT markets
  **0.25 % / 0.25 %** — reported consistently by third-party trackers
  (CoinGecko, CoinLaw, Korean exchange comparisons, all 2026); the
  official fee page did not render from this environment.
  **UNVERIFIED (third-party corroboration only — the settings UI must
  show Upbit's fees as unverified until the official page is checked
  from an unblocked network)**
- Fee per quote asset matters here: the KRW lane's edge math must not
  borrow the BTC/USDT-market 0.25 % nor assume 0.05 % globally.

## 7. Notes
- Error envelope is `{"error":{"name":<code>,"message":…}}`.
- Korean exchanges enforce KRW-lane identity checks for trading but
  NOT for public market data — nothing here needs an account.
- Connector complexity: MEDIUM (batched orderbook sweep instead of one
  bulk call; quote-first market ids; three quote currencies).
