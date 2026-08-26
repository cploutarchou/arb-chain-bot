# Market-Data Analysis — Local Order-Book Engineering

Status: RESEARCHED + ANALYSIS (protocol facts from `exchanges.md`, accessed
2026-08-26). This document turns the per-exchange protocol survey into the
engineering requirements for the platform's most critical subsystem.

---

## 1. Protocol taxonomy (what the book engine must abstract)

Across the seven venues there are exactly three initialization models and
four integrity models. The normalized book engine must express all of them
without leaking venue quirks upward.

**Initialization**
1. REST-init: buffer WS deltas, fetch REST snapshot, splice by update ID
   (Binance always; Gate classic channel; Bybit `orderbook.full`).
2. In-band snapshot: subscription delivers a snapshot message, then deltas
   (OKX, Bybit classic, Kraken, Coinbase, Bitget, Gate `obu`).
3. Snapshot-only feeds: every push replaces the book at fixed depth
   (Binance depth5/10/20, OKX books5, Bybit level-1, Bitget books1/5/15) —
   useful for cheap top-of-book, no gap detection, never for depth pricing.

**Integrity**
1. Update-ID chain, per stream: next event's first ID must extend the last
   applied ID (Binance U/u, Gate U/u, OKX prevSeqId→seqId strict chain,
   Bitget seq/pseq with a bracketing rule at snapshot boundaries).
2. Checksum over top-N levels (Kraken CRC32 top-10 with strict decimal
   formatting; Bitget CRC32 top-25 as a second layer). OKX's checksum is
   DEPRECATED (2026) — its chain is now the only mechanism.
3. Connection-global sequence counter across channels (Coinbase
   `sequence_num`) — requires a dedicated market-data connection to stay
   meaningful.
4. Reset-signal only (Bybit classic: `u=1` restart marker, unsolicited
   snapshots) — **no per-message continuity guarantee; silent gaps are
   undetectable in-protocol** and must be mitigated out-of-protocol.

## 2. Book engine requirements derived from the taxonomy

- One exchange-agnostic `Book` structure (sorted price levels, decimal
  prices/quantities, absolute-quantity semantics, qty-0 delete, optional
  depth truncation to subscribed depth — Kraken requires it).
- Per-venue `SequenceValidator` hook deciding for each message: APPLY,
  DUPLICATE (drop), GAP (mark CORRUPTED → resync), RESET (replace book).
  The validator owns the venue's splice rule for REST-init flows.
- Per-venue optional `ChecksumValidator` (Kraken mandatory, Bitget
  secondary). Checksum computation needs the venue's exact string-building
  rules (leading-zero stripping, interleaving order) and therefore decimal
  string preservation end-to-end: **JSON numbers are decoded as strings/
  decimals, never float64** — this is required for correctness on Kraken
  and for money math everywhere.
- Out-of-protocol drift detection for blind-spot venues (Bybit classic):
  periodic REST depth cross-check comparing top-N levels within tolerance;
  drift ⇒ CORRUPTED ⇒ resync. Optional everywhere as a chaos-hardening
  feature (CCXT-independent snapshot as test oracle per `frameworks.md`).
- State machine per book: SYNCING → HEALTHY ⇄ STALE → CORRUPTED →
  (resync) SYNCING; DISCONNECTED on transport loss. Only HEALTHY books feed
  the scanner. STALE is age-driven (no update within threshold for a market
  that should tick); CORRUPTED is integrity-driven. Transitions emit events
  (metrics + circuit breakers subscribe).
- Timestamps per update: exchange_event_time (when provided),
  local_receive_time, processed_time → network latency, processing
  latency, book age. Venues without event timestamps degrade gracefully
  (age measured from receive time; data_quality_score reflects it).

## 3. Transport requirements

- One WS client per venue with: bounded reconnect backoff + jitter,
  subscription re-establishment, venue keep-alive discipline (Binance
  server-ping/pong within 1min; OKX literal "ping" <30s; Bybit op:ping
  ~20s; Kraken app-level ping ≤60s; Bitget literal "ping" 30s; Coinbase
  heartbeats channel; Gate spot.ping), and scheduled reconnect ahead of
  forced disconnects (Binance 24h).
- Respect connection/subscription budgets (Binance 300 attempts/5min, 1024
  streams/conn; OKX 480 sub-ops/hour/conn — batch subscriptions; Bitget 240
  subs/hour/conn, ≤100 conns/IP; Kraken ~150 conns/10min). Reconnect storms
  are a chaos-test scenario precisely because several venues temp-ban on
  excess.
- REST budgets reserved for: init snapshots (Binance weight-250 calls are
  expensive — batch and pace), instrument metadata refresh, server-time
  polls, drift cross-checks. Kraken's ~1 req/s public budget effectively
  forbids REST-based books — WS-first is mandatory platform-wide.
- Message decoding on the hot path: preallocated buffers, no reflection-
  heavy generic decoding, decimal-preserving parsing; per-venue payload
  quirks (Coinbase large snapshots need raised read limits; OKX 64KB max
  frame; Bybit JSON field order differs by category).

## 4. Clock discipline

Every venue exposes a cheap server-time endpoint (`/api/v3/time`,
`/api/v5/public/time`, `/v5/market/time` (ns), `/0/public/Time`,
`/api/v3/brokerage/time`, `/api/v2/public/time`, `/api/v4/spot/time`).
The clock manager polls each venue's endpoint plus OS/NTP state, maintains
a per-venue offset estimate with round-trip compensation, and publishes
clock quality. Degraded clock quality ⇒ latency measurements untrusted ⇒
data marked unsafe ⇒ qualification suspended (SKILL.md section 12).

## 5. Subscription strategy for triangular scanning

- Depth requirement comes from optimal-size search, not top-of-book: the
  planner needs enough levels to price the maximum configured trade size
  plus headroom. Default: the venue's mid-tier depth channel (Binance
  @depth 100ms full-diff stream; OKX books 400@100ms; Bybit 50@20ms
  primary with 200@100ms fallback; Bitget books ~200ms; Gate obu 50@20ms
  or 400@100ms).
- Subscribe only to markets that participate in at least one enabled
  triangle (the triangle topology drives the subscription set, refreshed on
  instrument/config changes) — not the whole exchange.
- Book age and cross-leg age spread are qualification inputs: a triangle is
  evaluated only when all three books are HEALTHY and their age spread is
  within threshold (three healthy books of very different ages still
  fabricate phantom edges).

## 6. Recording implications

- Record raw frames per venue+stream with local receive timestamps in
  append-only segments (compressed chronological files; rotation by
  size/time), so replay drives the SAME connector parsing and book engine
  code paths deterministically.
- Record REST snapshots used during init/resync with their request/response
  times — replay must reproduce the splice decision exactly.
- Metadata (segment index, venue, streams, time range, config version) goes
  to PostgreSQL (`market_recording_metadata`); raw frames never go row-wise
  into the DB.

## 7. Consequences carried into architecture

1. Connector interface split: `Transport` (dial/read/keepalive/reconnect) /
   `Decoder` (frames → typed events, decimal-preserving) /
   `SequenceValidator` + optional `ChecksumValidator` (venue rules) /
   exchange-agnostic `BookEngine` (apply/reset/health/versioning). Live and
   replay share everything from Decoder down.
2. Book versioning: every applied update increments a monotonically
   increasing local version per book; opportunities cite the exact
   book_version per leg (auditability requirement, SKILL.md section 19).
3. The blind-spot mitigation (REST drift cross-check) is a first-class,
   schedulable component, mandatory for Bybit-class venues, optional
   elsewhere.
4. Health, latency, and clock metrics are emitted from the book engine
   itself — the scanner consumes health, never raw transport state.
