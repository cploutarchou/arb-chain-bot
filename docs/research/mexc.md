# MEXC (spot) — Venue Research (T-056)

Status: RESEARCHED 2026-08-26 from primary sources (`www.mexc.com/api-docs/spot-v3/*`,
`mexcdevelop.github.io/apidocs/spot_v3_en/`, `github.com/mexcdevelop/websocket-proto`,
mexc.com announcements/learn pages) plus live keyless pulls of
`api.mexc.com/api/v3/{exchangeInfo,depth,time}`. Same template as
`exchanges.md` / `fees.md`; scored on the `final-platform-selection.md` §1
rubric. Items not pinned to a primary page are marked UNVERIFIED with what
was tried. No credentials were used.

Method caveat: MEXC publishes two "official" doc surfaces that disagree on
some numbers (the legacy single-page `mexcdevelop.github.io` SPA and the
per-page `www.mexc.com/api-docs` site that carries 2026 changelog entries);
conflicts are reported side by side, not silently resolved.

---

## 1. L2 order book — protocol and integrity

- **Transport**: `wss://wbs-api.mexc.com/ws`, **protobuf** frames wrapped in
  `PushDataV3ApiWrapper` (schemas in `github.com/mexcdevelop/websocket-proto`;
  `protoc` targets incl. Go, but no first-class Go package/example). The
  old JSON host `wss://wbs.mexc.com/ws` was discontinued 2025-08-04 —
  any pre-2025-08 guide or SDK is stale.
- **Channels**: `spot@public.aggre.depth.v3.api.pb@(100ms|10ms)@<symbol>`
  ("Diff. Depth Stream": incremental, absolute quantities, `0` deletes,
  fields `fromVersion`/`toVersion`, `sendTime`, `lastOrderCreateTime`);
  `spot@public.limit.depth.v3.api.pb@<symbol>@(5|10|20)` (snapshot-only,
  shallow). The `increase.depth` slug seen in older SDKs is no longer in
  the doc navigation (direct URL 404) although its `.proto` still exists —
  treat as legacy, burn-in test before use.
- **Reconciliation rule** (doc "How to properly maintain a local copy of
  the order book"): subscribe and buffer; note the first push's
  `fromVersion`; `GET /api/v3/depth?symbol=X&limit=5000` → `lastUpdateId`;
  refetch if `lastUpdateId` < first `fromVersion`; drop pushes with
  `toVersion <= lastUpdateId`; first kept push must satisfy
  `fromVersion <= lastUpdateId + 1` ("if fromVersion > lastUpdateId + 1
  the data is considered discontinuous"); thereafter each push's
  `fromVersion` must equal the previous `toVersion + 1`, else reinitialise.
  → **REST-init + version chain, Binance class. No checksum** anywhere.
- **Documented divergence caveat** (same page): "levels outside the
  snapshot that have not changed in quantity will not appear in
  incremental pushes" — depth beyond the 5000-level snapshot that never
  updates is invisible and undetectable. Deep legs on thin symbols are
  therefore less trustworthy than on Binance.
- **REST snapshot**: `/api/v3/depth`, `limit` default 100, max 5000; live
  response carries `lastUpdateId`, `bids`, `asks` and a `timestamp`
  (verified 2026-08-26).

## 2. WebSocket limits

- Connection valid ≤ **24 h**; server drops after **30 s** without a
  subscription and after **60 s** without data flow.
- **30 subscriptions per connection** (WS page and FAQ Q7) — vs Binance's
  1024; multi-triangle coverage needs connection sharding.
- Keep-alive `{"method":"PING"}`; cadence not stated (UNVERIFIED — tried
  introduction, WS-streams and protobuf pages).
- "WebSocket server access is limited to 100 requests/s" — per-IP vs
  per-connection scope not stated (UNVERIFIED). Connections-per-IP cap not
  documented (UNVERIFIED).

## 3. REST limits, errors, server time

| Item | legacy SPA | `mexc.com/api-docs` (2026) |
|---|---|---|
| `/api/v3/depth` weight | 1 | 3 |
| `/api/v3/exchangeInfo` | weight 10 | "25 IP requests per minute" |
| IP budget | 500 per 10 s **per endpoint** | **300 weight / 10 s shared** across IP-weighted endpoints |
| UID budget | 500 per 10 s per endpoint | 500 weight / 10 s shared |

Burn-in must measure the real budget from response headers before REST
init pacing is fixed (the live `exchangeInfo` `rateLimits` array is empty).
429 = "rate limit exceeded; the IP may be banned soon", with `Retry-After`;
bans escalate "from a minimum of 2 minutes to a maximum of 3 days". Order
placement + cancellation share a UID bucket of 12 req/s (changelog
2026-03-11/05-22); ≤ 500 open orders per account. Server time
`GET /api/v3/time` → `{"serverTime": ms}` (verified).

## 4. Orders

- `orderTypes` per symbol from `exchangeInfo` (live 2026-08-26, 2,115
  symbols): 2,080 × `[LIMIT, MARKET, LIMIT_MAKER]`, **35 × `[LIMIT,
  LIMIT_MAKER]` (no MARKET) — ETHUSDC is one of them**.
- The current New Order page lists no `timeInForce` parameter; IOC/FOK
  appear only in a 2022 changelog entry, and ccxt issue #25003 (third
  party) reports `timeInForce=IOC` being accepted but returned as GTC.
  → Model MEXC as **GTC-only LIMIT/MARKET/LIMIT_MAKER** until a burn-in
  proves IOC; the simulator's limit-IOC semantics do not transfer as-is.
- MARKET accepts `quantity` or `quoteOrderQty`.

## 5. Test environment

**None for spot.** Demo Trading is Futures-only (announcements/learn
pages); `POST /api/v3/order/test` only validates parameters. Same tier as
Gate/Kraken.

## 6. Keys

Scoped read/write permissions per domain (spot account, spot trade,
transfer, withdraw, contract mirrors — exact enum strings UNVERIFIED via
the summariser); **per-key trading-pair allowlist**; up to 10 IPs per key
(unbound keys expire after 90 days, renewable); 30 keys per account;
`POST /api/v3/apiKeyInfo` (2026-06-11) manages the IP allowlist.

## 7. Instrument rules (`exchangeInfo`, live 2026-08-26)

- `status` `"1"` online / `"2"` pause / `"3"` offline; `isSpotTradingAllowed`
  (2,011 of 2,115 true); `permissions: ["SPOT"]`.
- **Decimals-based precision** (`baseAssetPrecision`, `quoteAssetPrecision`,
  `quotePrecision`) — no PRICE_FILTER/LOT_SIZE; the only filter present on
  all 2,115 symbols is `PERCENT_PRICE_BY_SIDE` (price band, e.g. 0.5 % on
  BTCUSDT, 2 % on USDC pairs). A separate tick-size field was not found
  (UNVERIFIED — tried the doc page and live pulls).
- Minimums/maximums live on the symbol object: `baseSizePrecision` (min
  base qty), `quoteAmountPrecision` (min notional, LIMIT),
  `quoteAmountPrecisionMarket` / `maxQuoteAmountMarket` (MARKET bounds —
  can be far tighter than LIMIT: ETHUSDC max 100,000 vs 2,000,000),
  `maxQuoteAmount`.
- **`makerCommission` / `takerCommission` are published per symbol,
  unauthenticated** (see §8).

## 8. Fees

- Base schedule per `exchangeInfo` and the MEXC learn article
  (2025-12-11): **0 % maker / 0.05 % taker** on 1,742 symbols (all USDT
  majors incl. BTCUSDT, ETHUSDT, ETHBTC).
- **Zero on 272 symbols**: 245 USDC-quoted (incl. **BTCUSDC, ETHUSDC,
  USDCUSDT**), 16 EUR, 6 USDE, 5 USDT (e.g. EURUSDT); USD1-quoted pairs
  0.01 %/0.01 % (101). Whether these `exchangeInfo` rates apply to API
  flow is **UNVERIFIED**: every platform-wide "0 fees on all spot pairs"
  promotion found (2025-12-09 EU, 2025-12-22 year-end, 2025-12-30 fee
  update "until further notice", 2026-04-13 → 05-13 anniversary festival)
  explicitly excludes "API users"/"API trading users", market makers and
  institutions. The USDC-zone zeros may be a standing schedule rather than
  a promo — only a keyed `GET /api/v3/tradeFee?symbol=` (weight 20,
  `SPOT_ACCOUNT_READ`, one symbol per call) settles it.
- MX discount: hold ≥ 500 MX for 24 h → 50 % off (taker 2.5 bps) or MX
  deduction 20 % off; API eligibility UNVERIFIED.
- Fee asset convention UNVERIFIED (no page states received- vs
  spent-side); confirm from a fill's `fee`/`feeAsset`.
- `www.mexc.com/fee` renders as an empty JS shell to non-browser clients
  (same dead end as Gate).
- **3-leg taker**: pessimistic **15 bps** (3 × 5); **5 bps** for a
  USDT→BTC→USDC→USDT cycle if the USDC-zone zeros hold for API; 7.5 bps
  with MX holding if API-eligible. Even the pessimistic figure undercuts
  every venue in `fees.md` (Binance-with-BNB 22.5).

## 9. Universe (live 2026-08-26)

2,115 spot symbols, all status 1; quotes USDT 1,707, USDC 246, USD1 101,
BTC 21, EUR 16, ETH 8. The six-market recording set (BTCUSDT, ETHUSDT,
ETHBTC, BTCUSDC, ETHUSDC, USDCUSDT) exists and is tradeable (ETHUSDC
without MARKET).

## 10. Reliability / quality signals

Two official doc surfaces disagree on core limits; IOC/FOK documented
historically but absent now and reportedly coerced to GTC; per-symbol
order-type gating undocumented as a rule; protobuf codegen required with
no official Go package; empty `rateLimits` in `exchangeInfo`; ban
escalation well documented (a plus).

## 11. Connector complexity: MEDIUM-HIGH

Binance-class REST-init chain, plus protobuf decoding, a 30-sub cap,
the off-snapshot divergence caveat, conflicting limits to burn-in, and
TIF unreliability.

## 12. Score (/100, §1 rubric)

| Criterion (max) | MEXC | Why |
|---|---|---|
| Economics (25) | 22 | 15 bps pessimistic beats every venue; USDC-zone zeros could make it 5 bps; docked for API-eligibility being unverified and promo/schedule confusion |
| Market data (25) | 17 | Binance-class chain, no checksum, plus divergence caveat and 30-sub cap |
| Universe (15) | 12 | 2,115 pairs, all six core legs live; many thin listings |
| Dev/test env (10) | 1 | no spot testnet/demo; `order/test` validates only |
| API reliability (10) | 5 | conflicting doc numbers, IOC coercion reports, undocumented gating |
| Order types (5) | 2 | MARKET symbol-conditional; IOC/FOK effectively absent |
| Key security (5) | 4 | 10 IPs/key, per-pair key scoping, programmatic allowlist; enum unverified |
| Complexity (5) | 2 | MEDIUM-HIGH |
| **Total** | **65** | between Gate (63) and Bitget (73) / Bybit (74) |

## 13. Comparison — 3-leg taker and gap detectability

| Venue | 3-leg taker (bps) | Gap detectability |
|---|---|---|
| **MEXC** | **15** (5 via USDC zone if API-eligible; 7.5 with MX) | chain (REST-init `fromVersion/toVersion`), no checksum, off-snapshot blind spot |
| Binance | 22.5 (BNB) / 30 | chain (REST-init U/u), no checksum |
| OKX | 30 | strict chain (prevSeqId), checksum deprecated |
| Bybit | 30 | **blind spot** (no continuity rule) |
| Bitget | 30 / 24 (BGB) | chain + CRC32 (top-25) |
| Gate | 60 / 45 (GT) | chain (REST-init U/u or `obu`) |

**Bottom line**: MEXC is the cheapest venue on paper by a wide margin and
its core triangle exists, but it has no spot dev environment, the weakest
order-type realism (GTC-only in practice), protobuf-only market data with
a small subscription budget, and fee facts that only a keyed
`tradeFee` call can settle. It belongs in the Phase 21 scoring round as
a fee-driven challenger with named burn-ins: (1) API fee on BTCUSDC /
ETHUSDC / USDCUSDT and BTCUSDT via `tradeFee`; (2) IOC behaviour with a
throwaway order (paper platform: research only, never live); (3) real
IP/UID budget from response headers; (4) WS ping cadence and
connections/IP; (5) fee asset convention from a fill.

Sources (all accessed 2026-08-26): mexc.com/api-docs/spot-v3 (introduction,
change-log, websocket-market-streams/{diffdepth-stream,
partial-book-depth-streams, how-to-properly-maintain-a-local-copy-of-the-order-book,
protocol-buffers-integration}, market-data-endpoints/{order-book,
exchange-information}, spot-account-trade/new-order);
mexcdevelop.github.io/apidocs/spot_v3_en; github.com/mexcdevelop/{websocket-proto,
mexc-api-sdk}; api.mexc.com/api/v3/{exchangeInfo,depth,time} (live);
mexc.com announcements 17827791522393 (WS replacement), 17827791532659,
17827791532472, 17827791532212, campaigns/8thanniversary0fee1,
how-to-access-demo-trading-in-mexc-futures-5705980230169; mexc.com/learn
(spot fees calculator 2025-12-11; MX discounts; futures demo);
github.com/ccxt/ccxt/issues/25003 (third party, IOC coercion).
