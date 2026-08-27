# Final Platform Selection

Status: DECIDED (synthesis of `exchanges.md`, `fees.md`, `market-data.md`,
`frameworks.md`, `triangular-constraints.md`; research accessed 2026-08-26;
§7 research debt re-verified 2026-08-26 from a network-enabled host — T-047).
The re-verification changed no decision: Binance stays first (its numbers
were confirmed), OKX stays second (its integrity model was confirmed and its
"UNVERIFIED" items resolved), Kraken's elimination got stronger (Tier 1 taker
is now 0.80 %).

Decisions:
- **First exchange: Binance** (score 90/100)
- **Second exchange: OKX** (77/100) — implemented only after Binance meets
  the first-exchange definition of done (SKILL.md section 79), and re-verified
  against current docs at that time
- **Integration approach: native Go connectors + our own book engine**;
  CCXT offline-only as metadata/test oracle; Hummingbot rejected
- **Stack: Go / Next.js+TypeScript+Tailwind / PostgreSQL / WebSocket /
  OpenTelemetry / Docker Compose** (per SKILL.md section 8 — research found
  no reason to deviate; no Kafka/K8s/ClickHouse/NATS)

---

## 1. Scoring rubric (/100)

Weights follow the constraint analysis (`triangular-constraints.md` §11):
feed correctness and effective fees dominate; headline volume matters only
through visible depth.

| Criterion | Weight |
|---|---|
| Economics: API-usable 3-leg fee cost, promo pairs, per-pair fee APIs | 25 |
| Market-data quality: integrity detectability, cadence/depth, snapshot discipline | 25 |
| Triangle universe: pair count, stablecoin breadth, depth/liquidity | 15 |
| Dev/test environment fidelity (spot testnet/demo) | 10 |
| API reliability: limits, bans, docs quality | 10 |
| Order-type support for realistic simulation (market, limit IOC/FOK) | 5 |
| API-key security (permission granularity, IP allowlist) | 5 |
| Connector complexity (lower = better) | 5 |

## 2. Scores

| Criterion (max) | Binance | OKX | Bybit | Bitget | Gate | Kraken | Coinbase |
|---|---|---|---|---|---|---|---|
| Economics (25) | 24 | 16 | 16 | 20 | 10 | 4 | 1 |
| Market data (25) | 20 | 22 | 18 | 19 | 20 | 21 | 21 |
| Universe (15) | 14 | 10 | 8 | 10 | 13 | 10 | 6 |
| Dev/test env (10) | 10 | 9 | 9 | 6 | 1 | 1 | 2 |
| API reliability (10) | 9 | 7 | 8 | 6 | 7 | 5 | 7 |
| Order types (5) | 5 | 5 | 5 | 5 | 5 | 4 | 4 |
| Key security (5) | 5 | 5 | 5 | 4 | 4 | 5 | 5 |
| Complexity (5) | 3 | 3 | 5 | 3 | 3 | 3 | 5 |
| **Total** | **90** | **77** | **74** | **73** | **63** | **53** | **51** |

Notable judgments behind the numbers:
- **Economics is eliminatory, not just a score.** Kraken (240 bps 3-leg since 2026-07-09) and
  Coinbase Advanced (360 bps) cannot produce net-positive taker cycles at
  base tier against typical sub-10 bps deviations, whatever their API
  quality. Their high market-data scores are moot for this strategy.
- **Binance economics are uniquely strong**: 22.5 bps with BNB, rotating
  zero-fee promo pairs that can cut one or two legs to zero when live
  (none at Regular tier in a liquid triangle on 2026-08-26), and per-pair
  account fee APIs to keep the engine honest.
- **OKX vs Bybit for #2**: identical bot economics (30 bps). OKX wins on
  feed integrity — its strict prevSeqId chain makes every gap detectable,
  while Bybit's classic channel has a documented-by-omission blind spot
  (no continuity rule; silent gaps undetectable in-protocol). For a
  platform whose first principle is book correctness, detectability
  outranks Bybit's simpler connector and official SDK — especially since
  `frameworks.md` already committed us to hand-written connectors, making
  SDK availability a minor convenience. Bybit (74) and Bitget (73) are
  near-ties for #3; that call is deferred to Phase 21 with fresh research.
- Reconciliation with `frameworks.md`: its "Kraken or Bybit for #2" leaned
  on Go-SDK availability before fee data landed. Kraken is fee-eliminated;
  with connectors hand-written by decision, the SDK criterion collapses,
  and OKX's integrity model prevails.

## 3. First exchange: Binance — implementation profile

- Feed: `<symbol>@depth@100ms` diff stream; REST snapshot
  `/api/v3/depth?limit=5000` (weight 250 — pace and batch inits); splice
  rule per official docs (buffer, drop `u <= lastUpdateId`, require chain
  continuity `U = prev u + 1`, else CORRUPTED → resync). No checksum —
  the U/u chain plus optional REST drift cross-checks are the integrity
  story.
- Ops constraints: 24h forced disconnect (scheduled pre-emptive reconnect),
  server ping/pong within 1min, 1024 streams/connection, 300 connection
  attempts/5min, 5 incoming client messages/s/connection, 6000 weight/min
  REST budget, 429/418 discipline (all re-verified 2026-08-26).
- Market-data host `data-stream.binance.vision` for public feeds.
- Instruments: single `exchangeInfo` call → PRICE_FILTER / LOT_SIZE /
  NOTIONAL filters into normalized InstrumentRules.
- Fees: runtime refresh from `/sapi/v1/asset/tradeFee` +
  `/api/v3/account/commission?symbol=` (captures BNB discount and promo
  zero-fee pairs); fee charged in received asset (or BNB) — modeled
  per leg.
- Dev environment: **Binance Demo Mode** (`demo-api.binance.com`,
  `demo-stream.binance.com`) — live-equivalent features/filters with
  realistic data — primary development target; spot testnet as fallback.
  Public market data requires no credentials at all.
- Starting assets (initial config): USDT and USDC primary; FDUSD only if a
  zero-fee promo makes its triangles structurally cheapest — as of
  2026-08-26 no such Regular-tier promo is live (fees.md), so FDUSD is not
  enabled; BTC/ETH/BNB as intermediates. Revisit from measured data.

## 4. Second exchange: OKX — readiness notes (Phase 20, not now)

Re-verified 2026-08-26 (okx-connector-checklist.md §14):

- Feed: `books` 400 levels @100 ms, in-band snapshot (`prevSeqId = -1`),
  strict `prevSeqId == previous seqId` chain per instId; keep-alive after
  ~60 s idle is an empty update with `seqId == prevSeqId`; a maintenance
  reset is an update with `seqId < prevSeqId` after which the rule
  resumes; checksum deprecated in production since 2026-06-23 (present,
  always 0). All confirmed from the primary docs and a live capture.
- Demo trading via `x-simulated-trading: 1` against `openapi.okx.com` /
  `wspap.okx.com`; sub-op budget 480 requests/hour/connection counts
  requests, not args, so batched subscriptions are cheap; 3 connection
  attempts/s/IP; 30 s idle timeout with text ping/pong; service-upgrade
  disconnects are announced 60 s ahead by `event: notice / code 64008`.
- REST cross-check: `market/books` 40 req/2 s/IP (`sz` ≤ 400),
  `books-full` 10 req/2 s/IP (≤ 5000).
- Fees: 8/10 bps Regular; ladder is volume/assets based with **no OKB
  tier** on the current schedule; fee charged in the received asset.
- Universe: 1,381 live spot instruments; the six-market recording set
  exists on OKX; `BTC-USDC`/`ETH-USDC` sit on a unified USD book
  (`tradeQuoteCcyList`).
- No official Go SDK — connector fully hand-written (already our model).

## 5. Technology approach (confirmed)

Per `frameworks.md`: native Go market-data connectors behind our own
normalized abstraction (Transport / Decoder / SequenceValidator /
BookEngine split per `market-data.md` §7); deterministic replay drives the
same code paths as live. CCXT (Go, pinned) allowed only in offline tooling:
instrument-metadata cross-checks and independent snapshot oracles in
integration tests. Hummingbot rejected (no maintained triangular strategy,
1s tick, process-hop bridging).

Stack: Go (1.24+), Next.js + TypeScript + Tailwind for the console,
PostgreSQL 16, backend WebSocket for real-time UI, OpenTelemetry +
Prometheus-compatible metrics, Docker Compose for local dev. Redis is NOT
adopted at this stage (no measured need; the hot path is in-process
memory). No Kafka/Kubernetes/ClickHouse/NATS/Elasticsearch.

## 6. Honest expectations (carried from constraints analysis)

Binance's liquid triangles are patrolled by fee-advantaged, co-located
firms; at 22.5–30 bps taker cost and retail latency, most detected raw
deviations must be rejected, and measured net edge may prove negative —
that is a valid research outcome the platform must report faithfully. The
economically interesting surface is: zero/discount-fee promo legs, less
crowded triangles, and volatile intervals — with the risk engine and
pessimistic simulation preventing self-deception. No strategy here is
guaranteed, risk-free, or certainly profitable.

## 7. Re-verification policy

Fees, promo pairs, rate limits, and channel specs rotate. Before Phase 4
(connector build) starts, and again at every phase boundary that touches a
venue: re-pull the venue's fee/instrument endpoints at runtime (never
hardcode), and diff the connector spec against current official docs.

### 7.1 Round-1 research debt — status after the 2026-08-26 re-verification

| Item (round 1: UNVERIFIED) | Result | Source, accessed 2026-08-26 |
|---|---|---|
| OKX OKB fee ladder | **RESOLVED** — current schedule tiers by 30-day volume or assets on platform; no OKB tier (EU regional page) | okx.com/fees embedded `feeDataInfo` JSON; okx.com/help/trading-fee-rules-faq |
| OKX `market/books` rate limit | **RESOLVED** — 40 req/2 s/IP (`sz` ≤ 400); `books-full` 10 req/2 s/IP | okx.com/docs-v5/en "Get order book" / "Get full order book" |
| Bitget spot-demo coverage | **DEMOTED → runtime-verified assumption** (demo assets SUSDT/SBTC/SETH/SUSDC and `paptrading: 1` strongly imply spot, but the current UTA v3 doc tree has no demo-trading section; old paths 404) — burn-in: connect to `wss://wspap.bitget.com` and subscribe a spot `books` channel | bitget.com/api-doc/uta (browser) |
| Bitget per-endpoint REST limits | **RESOLVED (v3)** — UTA v3 `GET /api/v3/market/orderbook` 20 req/s/IP, `limit` ≤ 1000, numeric JSON prices; account table 10 req/s default → 60–100 req/s by VIP; v2 doc paths now redirect to v3 | bitget.com/api-doc/uta/public/OrderBook, /uta/rate-limit (browser) |
| Coinbase private REST rps | **DEMOTED → runtime-verified assumption** (~30 req/s/IP per secondary sources) — burn-in: measure with a read-only key | docs.cdp.coinbase.com changelog (did not render) |
| Coinbase per-connection subscription cap | **PARTIAL** — 8 connections/s/IP and 8 unauthenticated msgs/s/IP confirmed; no per-connection subscription cap is documented → runtime-verified assumption | docs.cdp.coinbase.com …/websocket/websocket-rate-limits |
| Gate per-IP WS caps | **DEMOTED → runtime-verified assumption** — the WS reference (browsed) publishes no per-IP connection/subscription cap, only order-op rate-limit headers; burn-in: ramp connections until refused | gate.com/docs/developers/apiv4/ws/en (browser) |
| Kraken REST depth max | **RESOLVED** — `count` maximum 500 | docs.kraken.com/api/docs/rest-api/get-order-book |

Additional corrections found while re-verifying (details in fees.md /
exchanges.md): Kraken Tier 1 fees are 0.40 %/0.80 % since 2026-07-09 (3-leg
240 bps); Binance's U/u rule is two-phase (sync bracketing vs steady-state
gap test); Binance WS allows 5 incoming client messages/s/connection;
Binance's `account/commission.discount` is a multiplier, not a rebate
fraction; no Regular-tier zero-fee promo pair exists in a liquid Binance
triangle as of 2026-08-26 (KGST/USDT only, to 2026-08-31) — the live
FDUSD program is zero-maker only, taker at standard rates (browser-
verified 2026-08-27 on binance.com/en/fee/tradingPromote); OKX's
`books-rpi` channel, four-field size caps, price bands and unified-USD quote
grouping are new since round 1.

### 7.2 Standing runtime-verified assumptions

Everything demoted above plus: Kraken public-endpoint rate (~1 req/s/IP,
not on the rate-limit guide); Coinbase fee tiers (table behind login;
confirm via `transaction_summary`); Gate VIP0 20/20 bps and GT discount
(pages 403); OKX demo market data being live-mirrored; OKX seqId-reset
behaviour observed across a real maintenance window (documented, not yet
captured). Each is tied to a named burn-in check in
okx-connector-checklist.md §14 or the venue's connector task.
