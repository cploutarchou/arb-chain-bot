# Resource: Architecture

Authoritative documents: `docs/architecture.md`, `docs/data-flow.md`.
This resource is the working brief for agents; read those docs for detail.

Load-bearing decisions (do not re-litigate without principal-architect):
- Single `arbd` binary, componentized in `internal/app`; thin `cmd/` mains
  enable subsets. No IPC bus (no Redis/NATS/Kafka) — in-process bounded
  channels.
- Hot path: WS frame → decode → sequence-validate → book apply →
  dirty-queue → affected-triangle re-price → risk gate → opportunity
  event. Nothing on this path touches DB/AI/Telegram/frontend/network
  logging.
- Books: single writer per book (its feed goroutine); readers take
  immutable top-K `BookView` copies under a short RWMutex; dirty markets
  coalesce into a bounded queue drained by an evaluator pool.
- Persistence: async outbox with batching writer; overflow trips a
  breaker, never blocks the hot path. Raw frames go to append-only
  segment files, not the DB.
- Time: `Clock` interface everywhere; recorded clock + seeded RNG in
  REPLAY/BACKTEST (determinism per SKILL.md §64).
- All money math: shopspring/decimal. float64 on financial paths = P0.
- Modes: MARKET_DATA / RECORD / REPLAY / BACKTEST / PAPER / SHADOW select
  time source, executor, and started components.

Module path: `github.com/cploutarchou/arb-chain-bot`.
Package map and interfaces: `docs/architecture.md` §3–§4.
Failure handling: `docs/architecture.md` §15 (safe default: DO NOTHING).
