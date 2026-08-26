# Final Platform Selection

Status: DECIDED (synthesis of `exchanges.md`, `fees.md`, `market-data.md`,
`frameworks.md`, `triangular-constraints.md`; research accessed 2026-08-26)

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
- **Economics is eliminatory, not just a score.** Kraken (120 bps 3-leg) and
  Coinbase Advanced (360 bps) cannot produce net-positive taker cycles at
  base tier against typical sub-10 bps deviations, whatever their API
  quality. Their high market-data scores are moot for this strategy.
- **Binance economics are uniquely strong**: 22.5 bps with BNB, active
  zero-fee promo pairs that can cut one or two legs to zero, and per-pair
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
  attempts/5min, 6000 weight/min REST budget, 429/418 discipline.
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
- Starting assets (initial config): USDT and USDC primary; FDUSD enabled
  where zero-fee promo pairs make its triangles structurally cheapest;
  BTC/ETH/BNB as intermediates. Revisit from measured data.

## 4. Second exchange: OKX — readiness notes (Phase 20, not now)

- Feed: `books` 400 levels @100ms, in-band snapshot, strict
  prevSeqId→seqId chain; checksum deprecated (2026) so the chain
  implementation must be exact, including seqId repeats on keep-alives and
  reset-lower after maintenance.
- Demo trading via `x-simulated-trading: 1`; sub-op budget
  (480/hour/connection) requires batched subscriptions.
- No official Go SDK — connector fully hand-written (already our model).
- Re-verify fee ladder (OKB tiers) and rate limits from official docs when
  Phase 20 begins; both carried UNVERIFIED items in this round.

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
UNVERIFIED items in this round (OKX OKB ladder and current books rate
limit; Bitget spot-demo coverage and per-endpoint limits; Coinbase
private-REST rps and per-connection subscription cap; Gate per-IP WS caps;
Kraken REST depth max) are tracked as research-debt tasks in
docs/MASTER_PLAN.md.
