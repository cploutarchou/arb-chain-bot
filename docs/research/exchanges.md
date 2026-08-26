# Exchange Comparison — Spot APIs for Triangular Arbitrage

Status: RESEARCHED (official docs/SDKs where reachable, accessed 2026-08-26);
RE-VERIFIED 2026-08-26 from a network-enabled host (T-047): Binance, OKX,
Kraken, Bybit and Coinbase items re-pulled from primary sources and marked
"[re-verified 2026-08-26]" inline; OKX additionally verified against a live
`books` capture (see okx-connector-checklist.md §14). Items that still could
not be pinned to a primary page are labelled "runtime-verified assumption".
Companion docs: `market-data.md` (engineering analysis), `fees.md` (economics),
`final-platform-selection.md` (scoring + decision).

Method (round 1): verified from official GitHub doc repos/SDKs (Binance,
Bybit, Gate, Coinbase, Bitget) and official-doc search reads (OKX, Kraken) —
several doc domains were egress-blocked from that environment. Round 2
(2026-08-26) re-pulled the primary pages directly; remaining gaps are named
"runtime-verified assumption" rather than left as bare UNVERIFIED marks.
Pair counts are approximate unless dated.

---

## Binance (spot)

- **L2 book**: Diff. Depth Stream `<symbol>@depth[@100ms]` (100ms or 1000ms).
  Snapshot+delta with **REST snapshot required**: buffer events, GET
  `/api/v3/depth?limit=5000` → `lastUpdateId`, discard events with
  `u <= lastUpdateId`, apply. [re-verified 2026-08-26, web-socket-streams.md
  "How to manage a local order book" — the rule is two-phase:] **sync
  phase**: buffer, note the first event's `U`; if snapshot `lastUpdateId`
  < that `U`, re-fetch the snapshot; drop events with `u <= lastUpdateId`;
  the first kept event must satisfy `U <= lastUpdateId+1 <= u` (i.e.
  `lastUpdateId` within `[U;u]`). **Steady state**: ignore events with
  `u` < local update id; if `U` > local id + 1 you missed events →
  discard the book and restart; normally `U` of the next event equals
  the previous `u` + 1. Absolute quantities; qty 0 deletes. **No
  checksum.**
- **WS limits** [re-verified 2026-08-26]: 300 connection attempts/5min/IP;
  1024 streams/connection; **24h forced disconnect**; server ping every
  20s / pong within 1min; **5 incoming messages/second per connection**
  (ping, pong and JSON control messages all count; violators are
  disconnected, repeat offenders banned). Market-data host
  `wss://data-stream.binance.vision` available.
- **REST** [re-verified 2026-08-26]: depth weight 5/25/50/250 for limits
  ≤100/≤500/≤1000/≤5000 (limit capped at 5000); request-weight budget
  6000/min/IP (raised from 1200 on 2023-08-25, unchanged since); 429 on
  excess, 418 after continued abuse, both with Retry-After; public
  market data also served from `https://data-api.binance.vision`.
- **Server time**: `/api/v3/time` (weight 1).
- **Orders**: MARKET, LIMIT, LIMIT_MAKER; TIF GTC/**IOC/FOK**.
- **Test envs**: spot testnet (`testnet.binance.vision`, synthetic data,
  monthly resets) and **Demo Mode** (`demo-api.binance.com/api`,
  `wss://demo-stream.binance.com/ws` and `:9443/ws`) — "always has the
  same features as the live exchange", identical limits/filters, prices
  and books "similar to the live exchange" (explicitly not equal to real
  data) [re-verified 2026-08-26, demo-mode/general-info.md].
- **Keys**: HMAC/RSA/Ed25519; read-only and spot-trade toggles, withdrawal
  separate; IP allowlisting supported.
- **Universe**: ~1,300–1,500 spot pairs; metadata via single
  `/api/v3/exchangeInfo` (PRICE_FILTER, LOT_SIZE, NOTIONAL).
- **Fees (from fees.md)**: 10 bps taker base; 7.5 with BNB; zero-fee promo
  pairs rotate (none in a liquid Regular-tier triangle on 2026-08-26).
  3-leg: 30 / 22.5 / lower only while a promo leg exists.
- **Watch-outs**: only major venue with a mandatory REST+buffer init dance
  (classic desync source); no checksum → gaps detectable only via the U/u
  chain.
- **Connector complexity: MEDIUM.**

## OKX (v5 spot)

Re-verified 2026-08-26 against `www.okx.com/docs-v5/en/` (full page pulled
raw, 5.2 MB), the OKX help centre, the fee page's embedded schedule, the
public REST API and a 100-second live capture of the `books` channel
(BTC-USDT, ETH-BTC, USDC-USDT: 3 snapshots, several hundred updates, 0
chain breaks).
Item-level record: `okx-connector-checklist.md` §14.

- **L2 book**: `books` (400 levels, in-band snapshot then incremental
  pushes every 100 ms; measured update spacing min/median/max
  100/100/300 ms); `books5` snapshot-only; `bbo-tbt` (10 ms top of
  book); `books-l2-tbt` / `books50-l2-tbt` (10 ms, VIP4+, error `64003`
  otherwise); new `books-rpi` (400 levels, 100 ms, rows
  `[price, totalQty, nonRpiQty, count]`, supersedes `books-elp`).
  Envelope `{arg:{channel,instId}, action: "snapshot"|"update",
  data:[{asks,bids,ts,checksum,prevSeqId,seqId}]}`; rows are 4-element
  string arrays `[price, size, "0" (deprecated, always "0"), numOrders]`
  with size absolute in base currency, `"0"` deletes; `seqId`/`prevSeqId`
  are JSON **integers** (>2^32 — int64), `ts` a string. Sequencing:
  **`prevSeqId` must equal the previous message's `seqId`** (per instId;
  the same sequence is served on every connection); snapshot has
  `prevSeqId = -1`; **keep-alive after ~60 s idle** = update with empty
  `asks`/`bids` and `seqId == prevSeqId`; **maintenance reset** = an
  update whose `seqId` is *smaller* than its `prevSeqId`, after which the
  regular rule resumes (both documented with a worked example).
  **CRC32 checksum deprecated** — production since 2026-06-23 (demo
  2026-06-02): field still present, fixed to `0` (every captured
  frame carried `checksum: 0`). No REST snapshot needed; resync =
  unsubscribe/resubscribe. Push order across book channels on one
  connection is fixed and documented; frames from different instIds are
  interleaved in arrival order. Docs recommend <30 depth channels per
  connection.
- **WS limits** [primary]: 3 connection attempts/sec/IP; 480
  subscribe/unsubscribe/login **requests** per hour per connection (a
  multi-arg subscribe counts once — confirmed live: one op, three acks);
  30 s idle cut — client sends literal `"ping"`, expects `"pong"`; no
  fixed periodic disconnect, but a **60 s advance `event: "notice"`,
  `code: "64008"`** precedes service-upgrade disconnects on
  `/ws/v5/public`, `/private` (and `/business` since 2026-06-11). The
  "30 connections per channel per sub-account" cap applies to private
  channels only. Public `books` needs no login (verified keyless).
  `instruments` channel on `/ws/v5/public` pushes rule changes
  (`tickSz`, `minSz`, `maxMktSz`, state, listTime).
- **REST** [primary]: `/api/v5/market/books` (`sz` ≤400, cache updated
  every 50 ms, **40 req/2 s per IP**); `/api/v5/market/books-full` (`sz`
  ≤5000, updated once a second, **10 req/2 s per IP**); REST book rows
  carry `seqId` but **no `checksum` field at all**;
  `/api/v5/public/instruments` 20 req/2 s per IP+instType. Envelope
  `{code, msg, data}`; rate limiting returns `50011` either as HTTP 200
  or HTTP 429 (`Too Many Requests`); `50013` = 429 systems busy; `51001`
  = unknown instrument. No documented ban duration.
- **Server time**: `/api/v5/public/time` (ms string in `data[0].ts`).
- **Orders**: market, limit, post_only, **fok, ioc**, optimal_limit_ioc.
- **Test env**: demo trading — REST `https://openapi.okx.com` (hosts
  differ from production) with header `x-simulated-trading: 1` and demo
  keys created in the Demo Trading personal centre; WS
  `wss://wspap.okx.com:8443/ws/v5/{public,private,business}`. Whether
  demo market data mirrors live remains a runtime-verified assumption.
- **Keys**: passphrase+secret; Read/Trade/Withdraw split; IP allowlist.
- **Universe** [live 2026-08-26]: **1,381 spot instruments, all `live`**;
  quote coverage USDT 395, USD 291, EUR 267, USDC 265, TRY 128, BTC 3.
  The recording set `BTC-USDT`, `ETH-USDT`, `ETH-BTC`, `BTC-USDC`,
  `ETH-USDC`, `USDC-USDT` all exist and are live. Metadata
  `/api/v5/public/instruments` → `tickSz`, `lotSz`, `minSz` (base),
  `maxLmtSz`/`maxMktSz` (base) **and** `maxLmtAmt`/`maxMktAmt`
  (quote-denominated caps), price-band fields
  (`initPxLmtPct`/`maxPxLmtPct`/`floatPxLmtPct`), `state`
  (`live`/`suspend`/`preopen`/`test`), and `tradeQuoteCcyList` —
  `BTC-USDC`/`ETH-USDC` list `["USDG","USD","USDC","RLUSD"]` (unified USD
  order book, 2025-08-20 changelog) while no `BTC-USDG` instId exists.
  Still **no spot min-notional field**.
- **Fees** (fees.md): 8/10 bps Regular; ladder by 30-day volume or
  assets on platform — **no OKB-holding tier** on the current schedule.
  3-leg 30 bps.
- **Watch-outs**: seqId reset-lower is *legal* and must not be treated
  as corruption; keep-alive equality must not be treated as a gap;
  `books` vs REST structs differ (`checksum` present-but-zero vs absent);
  four size caps plus price bands; unified-USD quote grouping; no
  official Go SDK.
- **Connector complexity: MEDIUM.**

## Bybit (v5 spot)

- **L2 book**: `orderbook.{depth}.{symbol}` — spot depths 1 (10ms,
  snapshot-only), **50 (20ms)**, 200 (100ms), 1000 (200ms). In-band snapshot
  then deltas (0 = delete); **`u=1` means service-restart snapshot →
  overwrite**; fresh snapshots can be re-sent anytime. `seq` compares
  freshness across depths. **No checksum; no REST snapshot required.** New
  `orderbook.full` channel (200ms) is delta-only with REST init
  (`/v5/market/full-ob`) and a documented reset-scenario table.
- **WS limits**: ≤500 new connections/5min per domain; args ≤21,000 chars,
  spot ≤10 args/subscribe; client ping ~20s; idle cut at 10min.
- **REST**: `/v5/market/orderbook?category=spot` limit ≤1000; global HTTP
  600 req/5s/IP (403 ban on excess).
- **Server time**: `/v5/market/time` (sec + nano).
- **Orders**: Market/Limit; TIF GTC/**IOC/FOK**/PostOnly/RPI; market orders
  default quote-value qty (`marketUnit` selects base/quote).
- **Test envs**: testnet (`api-testnet.bybit.com`) plus Demo Trading
  (`api-demo.bybit.com`, demo funds endpoint; public data = mainnet).
- **Keys**: readOnly flag; SpotTrade permission group, withdrawal separate;
  IP binding supported — **unbound keys auto-expire (~90 days)**.
- **Universe**: ~500–700 spot pairs; metadata
  `/v5/market/instruments-info?category=spot` (`tickSize`, `basePrecision`,
  `minOrderAmt`).
- **Fees**: 10 bps flat for API flow (MNT/USDC discounts exclude API).
  3-leg 30 bps; crypto-fiat legs 60 bps.
- **Watch-outs** [re-verified 2026-08-26: docs still describe `u`=1 as a
  service-restart snapshot and `seq` only for cross-depth freshness]: the
  classic channel documents **no per-message continuity rule** — a silently dropped update is undetectable except via resets;
  mitigate with periodic REST cross-checks or the full-ob channel; JSON
  field order differs spot vs futures; level-1 repeats identical-`u`
  snapshots every 3s.
- **Connector complexity: LOW** (with the gap-detection blind spot noted).

## Kraken (spot)

- **L2 book**: WS **v2** `book` channel; depth 10/25/100/500/1000; snapshot
  on subscribe then updates; truncate to subscribed depth after each update.
  **CRC32 checksum in every update (top-10, strict decimal formatting
  rules); no sequence numbers** — mismatch ⇒ resubscribe. No REST snapshot
  needed.
- **WS limits**: ~150 connections/rolling 10min/IP (Cloudflare, 10min ban);
  app-level ping ≥ every 60s; many subscriptions per connection encouraged.
- **REST**: `/0/public/Depth` `count` maximum 500 [re-verified 2026-08-26,
  docs.kraken.com get-order-book]; public-endpoint rate is **not stated**
  on the Spot REST rate-limit guide (it documents only the per-key call
  counter: Starter 15 / Intermediate 20 / Pro 20, decay 0.33–1 per s) —
  the ~1 req/s/IP figure stays a runtime-verified assumption; REST
  snapshots effectively unusable at scale either way.
- **Server time**: `/0/public/Time`.
- **Orders**: market/limit (+stops); TIF GTC/**IOC**/GTD; **FOK added
  2026-05-12** (limit orders only) [change log, re-verified 2026-08-26];
  `validate=true` simulates placement.
- **Test env**: **no public spot sandbox** (qualified-clients-only test env;
  futures demo only).
- **Keys**: granular permissions; IP restriction supported.
- **Universe**: ~1,100–1,450 pairs incl. fiat; metadata
  `/0/public/AssetPairs` (`ordermin`, `costmin`, `tick_size`, decimals).
- **Fees**: **40/80 bps Tier 1 since 2026-07-09** [re-verified
  2026-08-26] — **3-leg 240 bps, economically non-viable** for taker
  cycles at base tier (stable/FX schedule 20/20; USDG 0/0.01%).
- **Watch-outs**: JSON numbers must be decoded as decimal/strings or
  checksums fail; XBT/X/Z legacy naming complicates triangle mapping.
- **Connector complexity: MEDIUM.**

## Coinbase Advanced Trade (spot)

- **L2 book**: `level2` channel on `advanced-trade-ws.coinbase.com`:
  snapshot then absolute-quantity updates (0 removes). Every message carries
  a **per-connection monotonically increasing `sequence_num`** across all
  channels — gap ⇒ resubscribe. No checksum; no REST snapshot needed; full
  book, no depth tiers.
- **WS limits**: market data needs no auth; use `heartbeats` channel;
  **8 new connections/sec/IP and 8 unauthenticated messages/sec/IP**
  [re-verified 2026-08-26, docs.cdp.coinbase.com websocket-rate-limits];
  no per-connection subscription cap is documented — runtime-verified
  assumption.
- **REST**: public `market/product_book`; ~10 req/s/IP public; private
  ~30 req/s/IP (secondary sources only; the CDP changelog page did not
  render the entry) — runtime-verified assumption.
- **Server time**: `/api/v3/brokerage/time`.
- **Orders**: market IOC, limit IOC (SOR), GTC/GTD, **FOK (beta)**,
  post-only, stop-limit.
- **Test env**: sandbox with **mocked/static responses**, no WS — of little
  use.
- **Keys**: CDP JWT keys (2min expiry per token); view/trade/transfer split;
  IP allowlist supported.
- **Universe**: ~400–600 products; metadata public `market/products`
  (`base_increment`, `quote_increment`, min sizes).
- **Fees**: 60/120 bps base — **3-leg 360 bps, prohibitive**.
- **Watch-outs**: BTC-USD snapshots exceed default WS frame limits (raise
  read limit); dedicate a connection to market data to keep `sequence_num`
  gap detection clean.
- **Connector complexity: LOW** (economics disqualify it anyway).

## Bitget (v2 spot)

- **L2 book**: `books` (full depth, in-band snapshot then updates, ~200ms;
  `books1` 10ms snapshot-only; `books5`/`books15` snapshots). Messages carry
  `seq`/`pseq` and **CRC32 checksum (signed int32, top-25 interleaved
  `bid1px:bid1sz:ask1px:ask1sz:…`)**; snapshot-to-delta bracketing rule:
  snapshot's `seq` must fall within `[pseq, seq]` of the first subsequent
  incremental; discontinuity or checksum failure ⇒ resubscribe. No REST
  snapshot needed.
- **WS limits**: 300 connection requests/IP/5min, ≤100 connections/IP;
  240 subscribes/hour/connection; literal `"ping"` every 30s.
- **REST**: orderbook limit ≤150; global 6,000 req/min/IP; per-endpoint
  numbers not retrievable (api-doc pages are client-rendered; 2026-08-26
  re-check found only a generic 20 req/s market-data figure in secondary
  sources) — runtime-verified assumption.
- **Server time**: `/api/v2/public/time`.
- **Orders**: limit/market; force **gtc/post_only/fok/ioc**.
- **Test env**: demo via `paptrading: 1` header + `wss://wspap.bitget.com`
  with "S"-prefixed demo assets (SUSDT/SBTC/SETH/SUSDC) — strong evidence
  of spot coverage but not stated on a retrievable primary page
  (2026-08-26) — runtime-verified assumption.
- **Keys**: secret+passphrase; read-only/trade split; IP allowlist.
- **Universe**: ~800–1,000+ pairs; metadata `/api/v2/spot/public/symbols`
  (decimals-based precision, `minTradeUSDT`).
- **Fees**: 10 bps base, 8 with BGB (API-eligible). 3-leg 30 / 24 bps.
- **Watch-outs**: unusual bracketing rule must be implemented exactly;
  checksum covers only top-25; docs quality below tier-1 (mixed v1/v2
  pages); seq resets during maintenance.
- **Connector complexity: MEDIUM.**

## Gate.io (APIv4 spot)

- **L2 book**: three generations: classic `spot.order_book_update`
  (100ms/1000ms, plus 20ms depth-20 variant) — **Binance-style REST-init**
  (`with_id=true` snapshot, `U <= baseID+1 <= u` rule, `U = prev u+1`
  continuity) with 2025 hybrid twist: inline **`full: true` snapshots** can
  arrive mid-stream; new **`spot.obu`** channel (levels 50 @20ms, 400
  @100ms) with in-band snapshots + U/u increments — preferred; plain
  `spot.order_book` periodic snapshots. **No checksum.**
- **WS limits**: ≤50 requests/sec/connection; per-IP caps unpublished —
  gate.com/gate.io doc and help domains answer HTTP 403 to non-browser
  clients (re-checked 2026-08-26), so this stays a runtime-verified
  assumption.
- **REST**: `order_book` limit ≤100 (`with_id=true` for sync); public 200
  req/10s per endpoint per IP.
- **Server time**: `/api/v4/spot/time`.
- **Orders**: limit/market; TIF **gtc/ioc/poc/fok** (market accepts ioc/fok
  only).
- **Test env**: futures testnet only — **no spot testnet**.
- **Keys**: per-category read-only vs read-write; withdrawal separate; IP
  allowlist.
- **Universe**: **largest of the seven, ~2,000–2,800+ pairs**; metadata
  single `/api/v4/spot/currency_pairs`.
- **Fees**: 20 bps base, 15 with GT. 3-leg 60 / 45 bps.
- **Watch-outs**: two-generation channel landscape; classic path must handle
  mid-stream full snapshots; 100-level REST cap makes WS-first mandatory.
- **Connector complexity: MEDIUM.**

---

## Summary matrix

| | Binance | OKX | Bybit | Kraken | Coinbase | Bitget | Gate |
|---|---|---|---|---|---|---|---|
| Book protocol | REST-init + U/u chain | in-band + seq chain | in-band + resets | in-band + CRC32 | in-band + conn seq | in-band + seq+CRC32 | REST-init or obu |
| Gap detectability | chain | chain (strict) | **blind spot** | checksum only | chain (conn-wide) | chain + checksum | chain |
| Best update cadence | 100ms | 100ms (10ms VIP) | **20ms @50 lvl** | real-time | real-time | 200ms (10ms L1) | 20ms @50 lvl |
| Spot test env | testnet + **demo** | demo | testnet + demo | none | mock only | demo (spot: assumed) | none |
| 3-leg taker (API) | **22.5–30 (0 promo legs)** | 30 | 30 | 240 | 360 | 24–30 | 45–60 |
| Spot pairs (~) | 1,300–1,500 | 1,381 (2026-08-26) | 500–700 | 1,100–1,450 | 400–600 | 800–1,000 | 2,000–2,800 |
| Complexity | MEDIUM | MEDIUM | LOW | MEDIUM | LOW | MEDIUM | MEDIUM |
