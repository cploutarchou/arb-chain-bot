# Resource: Order Book Engine

Authoritative documents: `docs/research/market-data.md` (taxonomy, venue
rules), `docs/architecture.md` §5–§6, §16 (state machine).
Implementation home: `internal/orderbook`, `internal/exchange/<venue>`.

Non-negotiables:
- Decimal-preserving decode: JSON prices/quantities parsed as strings into
  decimals; float64 anywhere in the pipeline is a P0 (breaks Kraken-style
  checksums and money math).
- Per-venue `SequenceValidator` returns APPLY / DROP (duplicate) / GAP
  (→ CORRUPTED, resync) / RESET (replace book). Binance rule: drop
  `u <= lastUpdateId`; require `U == last_u + 1` after init; splice via
  buffered events + REST snapshot (`lastUpdateId`).
- Health machine: SYNCING → HEALTHY ⇄ STALE → CORRUPTED → SYNCING;
  DISCONNECTED on transport loss. Only HEALTHY books feed the scanner.
  Age thresholds and age-spread across a triangle's three books are risk
  inputs.
- Book version increments on every applied event; opportunities cite
  book_version per leg.
- Absolute-quantity semantics: qty 0 deletes a level; venues with
  truncate-to-depth (Kraken) truncate after each update.
- Timestamps: exchange_event_time (if provided), local_receive_time,
  processed_time → latency + age metrics (atomic, hot-path safe).
- Out-of-protocol drift check (periodic REST top-N comparison) is
  mandatory for venues with undetectable gaps (Bybit classic), optional
  chaos-hardening elsewhere.
- Replay drives the SAME decoder/validator/book code with recorded frames
  and recorded clock.

Chaos scenarios every venue implementation must pass: disconnect,
reconnect storm, duplicate, loss, out-of-order, snapshot delay, REST
failure during resync, WS freeze (silent stall), clock skew, burst.
