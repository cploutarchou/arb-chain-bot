# Bithumb — public endpoint research (T-075)

Scope: PUBLIC (no API key) endpoints only, spot only (no perpetual
futures on this host). Access date for every citation: **2026-09-13**.
Legend as in docs/research/screener-endpoints.md (VERIFIED /
VERIFIED-ABSENT / UNVERIFIED). Planned collector:
internal/screener/venue/bithumb.go. Official docs at
apidocs.bithumb.com (markdown mirrors under `/docs/*.md` and
`/reference/*.md` — fetchable directly).

## 1. Spot bulk order book (bid/ask + size)
- `GET /public/orderbook/ALL` → `{status:"0000", data:{timestamp,
  payment_currency:"KRW", <COIN>:{order_currency, bids:[{price,
  quantity}…], asks:[{price, quantity}…]}, …}}` — **every KRW market's
  book in ONE request**, arrays sorted best-first. **VERIFIED** (live)
- `GET /public/orderbook/ALL_BTC` — same shape, `payment_currency:
  "BTC"`; the BTC-quoted lane exists. **VERIFIED** (live)
- Newer v1 quotation API: `GET /v1/orderbook?markets=KRW-BTC,BTC-ETH` →
  Upbit-shaped `[{market, timestamp, total_ask_size, total_bid_size,
  orderbook_units:[…]}]`; **15 levels per market when multiple are
  requested, 30 for a single one** (docs). **VERIFIED** (live call +
  reference page 호가-조회)
- Prices/quantities are JSON strings. **VERIFIED**

## 2. Spot bulk ticker / instruments
- `GET /public/ticker/ALL_KRW` and `ALL_BTC` →
  `{status:"0000", data:{<COIN>:{opening_price, closing_price,
  min_price, max_price, units_traded, acc_trade_value,
  prev_closing_price, units_traded_24H, acc_trade_value_24H,
  fluctate_24H, fluctate_rate_24H}, …}}` — 24 h statistics per coin, no
  bid/ask (the §1 orderbook is the quote source; `closing_price` is the
  last trade). The coin keys are the instrument universe. **VERIFIED**
  (live)
- `GET /public/ticker/ALL_USDT` → `{"status":"5500","message":"입력값을
  확인해 주세요."}` (HTTP 200) — **no USDT lane on api.bithumb.com**.
  **VERIFIED-ABSENT**
- No public tick/step/min-size fields anywhere; same treatment as
  Upbit §2. **UNVERIFIED**

## 3. Perpetuals
- No perpetual futures API on api.bithumb.com (Bithumb's derivatives
  live on a separate global product; out of scope for public market
  data here). **VERIFIED-ABSENT**

## 4. Currency / chain status — PUBLIC
- `GET /public/assetsstatus/ALL` → `{status:"0000",
  data:{<COIN>:{withdrawal_status:1, deposit_status:1}, …}}` — per-coin
  deposit/withdrawal flags. Shape **VERIFIED** (live); the numeric
  semantics (1 = active, 0 = stopped) follow the legacy docs and are
  treated as: only `1` counts as available, anything else suspended.
  **VERIFIED (shape) / UNVERIFIED (exact semantics — collector maps
  conservatively)**
- The newer `GET /v1/status/wallet` (`wallet_state`:
  working/paused/withdraw_only/deposit_only, `block_state`:
  normal/delayed/inactive) requires a JWT: live 401 `invalid_jwt`.
  **VERIFIED-ABSENT (public)** — reference page 입출금-현황.

## 5. Rate limits / bans
- **150 requests/second per IP per category** (public categories:
  현재가 ticker / 호가 orderbook / 촛대 candles / 체결 trades / 기타
  market-info); calls to endpoints in the same category count
  together; exceeding blocks that category for the IP until reset.
  **VERIFIED** (docs page api-요청-수-제한-안내)
- Collector gates (plan): orderbook category 150/s — the collector
  makes 2 requests per poll (ALL + ALL_BTC), nowhere near it.

## 6. Fees (regular tier)
- Official fee page (bithumb.com/react/info/fee/trade): KRW market
  maker **0.25 % → 0.04 %** and BTC market maker **0.25 % → free**
  via an opt-in program (국내 최저 수수료 신청), taker **0.25 %** both
  markets. **VERIFIED (official page; the discounted maker rates are
  opt-in — the fee table must carry the standard 0.25 %/0.25 %, with
  the opt-in noted, or paper P&L turns optimistic exactly like the
  token-discount defect fixed in T-057's review)**
- Per quote asset: KRW and BTC lanes differ only via the opt-in
  program; default table treats both as 0.25 %/0.25 %.

## 7. Notes
- Envelope: `{"status":"0000","data":…}`; non-"0000" status is an
  error (`5500` = bad input, live-verified).
- Bithumb's KRW lane + Upbit's KRW lane are the two same-quote Korean
  venues — the KRW cross-venue spread lane exists only once BOTH
  collectors ship.
- Connector complexity: LOW (true bulk endpoints; one request per
  payment currency; string decimals).
