# Exchange Comparison — Spot APIs for Triangular Arbitrage

Status: RESEARCHED (official docs/SDKs where reachable, accessed 2026-08-26)
Companion docs: `market-data.md` (engineering analysis), `fees.md` (economics),
`final-platform-selection.md` (scoring + decision).

Method: verified from official GitHub doc repos/SDKs (Binance, Bybit, Gate,
Coinbase, Bitget) and official-doc search reads (OKX, Kraken) — several doc
domains were egress-blocked from this environment. UNVERIFIED marks facts that
could not be pinned to an official source. Pair counts are approximate.

---

## Binance (spot)

- **L2 book**: Diff. Depth Stream `<symbol>@depth[@100ms]` (100ms or 1000ms).
  Snapshot+delta with **REST snapshot required**: buffer events, GET
  `/api/v3/depth?limit=5000` → `lastUpdateId`, discard events with
  `u <= lastUpdateId`, apply; ongoing rule: **restart if event `U` >
  localID+1** (each event's `U` should equal previous `u`+1). Absolute
  quantities; qty 0 deletes. **No checksum.**
- **WS limits**: 300 connection attempts/5min/IP; 1024 streams/connection;
  **24h forced disconnect**; ping every 20s / pong within 1min. Market-data
  host `wss://data-stream.binance.vision` available.
- **REST**: depth weight 250 at 5000 levels; request-weight budget
  6000/min/IP; 429/418 + Retry-After discipline.
- **Server time**: `/api/v3/time` (weight 1).
- **Orders**: MARKET, LIMIT, LIMIT_MAKER; TIF GTC/**IOC/FOK**.
- **Test envs**: spot testnet (`testnet.binance.vision`, synthetic data) and
  **Demo Mode** (`demo-api.binance.com` / `demo-stream.binance.com`) — same
  features/filters as live with realistic market data; best-in-class for
  paper development.
- **Keys**: HMAC/RSA/Ed25519; read-only and spot-trade toggles, withdrawal
  separate; IP allowlisting supported.
- **Universe**: ~1,300–1,500 spot pairs; metadata via single
  `/api/v3/exchangeInfo` (PRICE_FILTER, LOT_SIZE, NOTIONAL).
- **Fees (from fees.md)**: 10 bps taker base; 7.5 with BNB; active zero-fee
  promo pairs. 3-leg: 30 / 22.5 / lower with promo legs.
- **Watch-outs**: only major venue with a mandatory REST+buffer init dance
  (classic desync source); no checksum → gaps detectable only via the U/u
  chain.
- **Connector complexity: MEDIUM.**

## OKX (v5 spot)

- **L2 book**: `books` (400 levels, in-band snapshot then updates every
  100ms); `books5` snapshot-only; tbt channels (10ms) gated at VIP4/VIP5.
  Sequencing: **`prevSeqId` must equal previous `seqId`** (strict chain);
  seqId can repeat on keep-alives and reset lower after maintenance.
  **CRC32 checksum DEPRECATED as of May 2026** (field fixed to 0) — seq
  chain is now the only integrity mechanism. No REST snapshot needed.
- **WS limits**: 3 connection attempts/sec/IP; 480 sub/unsub/login per hour
  per connection; 30s idle timeout — send literal `"ping"`.
- **REST**: `/api/v5/market/books` (≤400) and `books-full` (≤5000);
  per-endpoint window limits (exact current numbers UNVERIFIED).
- **Server time**: `/api/v5/public/time`.
- **Orders**: market, limit, post_only, **fok, ioc**.
- **Test env**: demo trading via `x-simulated-trading: 1` header + demo keys;
  WS `wspap.okx.com`. Full spot support.
- **Keys**: passphrase+secret; Read/Trade/Withdraw split; IP allowlist up to
  20 IPs.
- **Universe**: ~550–1,000 spot pairs; metadata `/api/v5/public/instruments`
  (`tickSz`, `lotSz`, `minSz`; no spot min-notional).
- **Fees**: 8/10 bps maker/taker base; OKB-holding tier ladder (UNVERIFIED
  details). 3-leg ~30 bps.
- **Watch-outs**: checksum removal puts full weight on a strict prevSeqId
  implementation; seqId reset semantics after maintenance need explicit
  handling; no official Go SDK (community only).
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
- **Watch-outs**: the classic channel documents **no per-message continuity
  rule** — a silently dropped update is undetectable except via resets;
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
- **REST**: `/0/public/Depth` count ≤500 (high-confidence, re-verify);
  public endpoints ~**1 req/sec/IP** — REST snapshots effectively unusable
  at scale; private uses a decaying counter.
- **Server time**: `/0/public/Time`.
- **Orders**: market/limit (+stops); TIF GTC/**IOC**/GTD; **FOK added 2026**;
  `validate=true` simulates placement.
- **Test env**: **no public spot sandbox** (qualified-clients-only test env;
  futures demo only).
- **Keys**: granular permissions; IP restriction supported.
- **Universe**: ~1,100–1,450 pairs incl. fiat; metadata
  `/0/public/AssetPairs` (`ordermin`, `costmin`, `tick_size`, decimals).
- **Fees**: 25/40 bps base — **3-leg 120 bps, economically non-viable** for
  taker cycles at base tier (stable/FX schedule 20/20; USDG 0/0.01%).
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
  per-connection subscription cap UNVERIFIED.
- **REST**: public `market/product_book`; ~10 req/s/IP public (private
  number UNVERIFIED).
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
- **REST**: orderbook limit ≤150; global 6,000 req/min/IP (per-endpoint
  numbers UNVERIFIED).
- **Server time**: `/api/v2/public/time`.
- **Orders**: limit/market; force **gtc/post_only/fok/ioc**.
- **Test env**: demo via `paptrading: 1` header + `wspap.bitget.com`;
  futures-slanted docs — **spot demo coverage UNVERIFIED**.
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
- **WS limits**: ≤50 requests/sec/connection; per-IP caps unpublished
  (UNVERIFIED).
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
| Spot test env | testnet + **demo** | demo | testnet + demo | none | mock only | demo (spot UNVERIFIED) | none |
| 3-leg taker (API) | **22.5–30 (0 promo legs)** | ~30 | 30 | 120 | 360 | 24–30 | 45–60 |
| Spot pairs (~) | 1,300–1,500 | 550–1,000 | 500–700 | 1,100–1,450 | 400–600 | 800–1,000 | 2,000–2,800 |
| Complexity | MEDIUM | MEDIUM | LOW | MEDIUM | LOW | MEDIUM | MEDIUM |
