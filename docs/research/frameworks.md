# Framework Comparison — Native Go vs CCXT vs Hummingbot vs Hybrid

Status: RESEARCHED (official repos/docs, accessed 2026-08-26)
Decision: **A + thin D — native Go connectors; CCXT offline-only; Hummingbot rejected**

Scope: single-exchange triangular arbitrage, paper trading only, Go backend.
Hot path = locally maintained L2 books + deterministic recalculation;
sequence-reconciliation correctness is critical; 1–2 exchanges initially.

---

## 1. CCXT (Option B)

- Latest release **v4.5.75 (2026-08-21)**; ~103 exchanges REST, ~70+ WebSocket
  ("Pro"). **MIT license**, Pro included free.
- **Go is now an official target** (`github.com/ccxt/ccxt/go/v4`, Go 1.20+),
  including WebSockets: REST-only Go announced Feb 2025 (issue #25173), WS
  added in v4.5.9 (PR #26440, Oct 2025 inferred). The Go WS layer is ~10
  months old — CCXT's youngest, least battle-tested language target.
- All non-TypeScript languages are **machine-generated from the TS core via a
  regex-based transpiler** — not hand-written idiomatic Go. 2026 rough edges
  on record: inconsistent Go error handling on the WS path (#27978), Go CLI
  broken by structural changes (#28387).
- Order books: CCXT Pro maintains client-side snapshot+delta books with
  per-exchange nonce/CRC32 checksum handling — and this is its **historical
  soft spot**: recurring checksum/desync bugs per exchange (Bitget #18322,
  #21997; Kraken #24461 — a connection close drops *all* subscriptions;
  Bitfinex2 #17833; KuCoin #14567). Community workarounds often *disable*
  checksum validation — exactly the wrong failure mode for us.
- Latency: no authoritative benchmark found (UNVERIFIED quantitatively);
  architecturally, every message passes generic normalization, book age is
  hard to observe from outside (#26099), and resync/reconnect policy lives
  inside generated code.
- Dependency weight: one module vendoring 100+ generated exchange
  implementations to use one or two.

## 2. Hummingbot (Option C)

- Python + Cython engine; central clock ticks strategies **once per second**;
  asyncio WS listeners feed an OrderBookTracker. Apache 2.0. v2.16.0
  (Jul 2026 inferred).
- **Triangular arbitrage is NOT a maintained strategy.** Verified against
  `hummingbot/strategy/` on master: pure/perpetual/avellaneda market making,
  cross-exchange MM/mining, amm_arb, spot-perp arb, hedge, liquidity mining —
  no triangular. V1 strategies are legacy; V2 arbitrage controllers cover
  cross-venue arb only. Triangular exists only as an old blog tutorial and
  unmaintained community forks.
- Bridging to Go goes through hummingbot-api (REST/FastAPI) or an MQTT broker
  layer — bot-orchestration planes, not low-latency market-data buses. A
  Go-centric sub-tick recalculation design would demote Go to a satellite of
  a 1s-tick Python runtime.
- **Rejected** for this system.

## 3. Go-native building blocks (Option A)

| Exchange | Library | Status (2026-08-26) |
|---|---|---|
| Binance | adshao/go-binance (community, MIT) | v2.8.12, 2026-06-08 — active, de-facto standard; spot REST + WS depth diffs + testnet |
| Binance | binance/binance-connector-go (official, MIT) | Actively regenerated per-product modules (commits Aug 2026); REST + WS streams + WS API |
| Bybit | bybit-exchange/bybit.go.api (official, MIT) | v1.1.1, 2026-01-15; REST v5 + public WS orderbook.N |
| Kraken | krakenfx/api-go (official, MIT) | v2.0.0, 2025-07-02; REST + WS v2 with **checksum-validated L2/L3 book builder** |
| OKX | community only (amir-the-h/okex stale 2023; tigusigalpa/okx-go maturity UNVERIFIED) | **Weakest Go ecosystem of the majors** |
| multi | thrasher-corp/gocryptotrader | active but self-declared "not ready for production" |

Binance's Go ecosystem is excellent; Bybit and Kraken have official maintained
SDKs (Kraken's even ships checksum-aware books); OKX is community-only.

## 4. Trade-off summary

| Criterion | A: Native Go | B: CCXT Go | C: Hummingbot | D: Hybrid |
|---|---|---|---|---|
| Hot-path performance | Direct JSON → own structs | Generic normalization overhead; WS layer young | Cross-process + 1s tick — disqualifying | = A |
| Sequence/resync control | Total — we own gap detection, resync, checksums | Inside generated code; per-exchange bug history | None from Go | = A |
| Dependency weight | One WS lib + decimal lib (+ optional SDK) | 100+ generated exchanges in-module | Python runtime + brokers | A + CCXT offline |
| Replay/testability | Best: capture raw frames, replay deterministically | No documented frame-injection seam | Python-side only | = A + CCXT as oracle |
| Breadth (future) | Linear cost per exchange | Killer feature (100+) | 40+ connectors | Native where it matters |
| License | MIT throughout | MIT | Apache 2.0 | MIT/Apache |

Decisive factors:
1. Sequence-reconciliation correctness is our stated critical requirement —
   and it is precisely CCXT's recurring weak spot, in its least mature port.
2. We need 1–2 exchanges, which removes CCXT's main advantage (breadth).
3. Deterministic replay of raw frames into our own book engine is
   straightforward natively and unsupported through CCXT's abstraction.

## 5. Decision

**Native Go market-data connectors behind our own normalized abstraction.**

- Own small interfaces: a feed emitting typed snapshot/delta events; an
  exchange-agnostic book engine applying them with explicit sequence
  validation, driven identically by live frames and recorded frames.
- Exchange 1 (Binance, per `final-platform-selection.md`): use
  adshao/go-binance or the official connector for transport if convenient,
  but implement depth-diff synchronization ourselves (buffer diffs, REST
  snapshot, apply from `lastUpdateId`) — owning that logic is the point.
- Exchange 2 candidates: Kraken (official checksum-validated Go book builder
  usable directly or as reference) or Bybit (official SDK). OKX deprioritized
  until its Go ecosystem improves or we hand-write the connector.
- CCXT's legitimate offline roles (never on the hot path, pinned version):
  metadata cross-checks (precision/tick/fee data feeding triangle enumeration
  sanity tests), independent REST order-book snapshots in integration tests
  to detect book drift, and prototyping candidate exchange #3.
- Hummingbot: rejected outright (no maintained triangular strategy, 1s tick,
  process-hop bridging).

## Sources

All accessed 2026-08-26: github.com/ccxt/ccxt (README, releases v4.5.75 and
v4.5.9, issues #25173 #27978 #28387 #26099 #21997 #24461 #18322 #17833
#14567 #9347, Pro manual), pkg.go.dev/github.com/ccxt/ccxt/go/v4,
github.com/hummingbot/hummingbot (+releases, strategy tree, site docs,
hummingbot-api, brokers), medium.com/hummingbot triangular tutorial,
github.com/adshao/go-binance, github.com/binance/binance-connector-go,
github.com/bybit-exchange/bybit.go.api, pkg.go.dev/github.com/hirokisan/bybit/v2,
github.com/krakenfx/api-go, pkg.go.dev/github.com/amir-the-h/okex,
github.com/tigusigalpa/okx-go, github.com/thrasher-corp/gocryptotrader.
Caveat: GitHub omits the year on current-year dates; inferred years are
flagged inline. CCXT latency remains architecturally argued, not benchmarked
(UNVERIFIED).
