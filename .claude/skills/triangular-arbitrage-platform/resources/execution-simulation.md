# Resource: Execution Simulation

Authoritative documents: `docs/data-flow.md` §2 (cycle flow),
`docs/architecture.md` §4 (Executor boundary).
Implementation home: `internal/execution`, `internal/simulation`.

Boundary (never weakened): `Executor` implementations are PaperExecutor,
ReplayExecutor, SimulationExecutor, ShadowExecutor. `LiveExecutor` exists
and always returns `ErrLiveTradingDisabled` — no flag enables it.

Simulation realism requirements:
- Legs are sequential; each leg: submit-latency draw (seeded RNG) →
  re-read book at simulated fill time → fill by depth walk (VWAP; partial
  fills when depth insufficient; configurable achievable-depth haircut) →
  fee in the venue's fee asset → precision truncation → output feeds next
  leg. Never fill at detection price; never fill instantly.
- Latency model: configurable distributions per venue (submit, ack, fill),
  seeded; REPLAY uses recorded clock + same seed ⇒ reproducible.
- Order semantics: simulate limit-IOC per leg by default (bounded
  slippage, may fail/partial) with market-order mode as an alternative
  (fills deeper, unbounded slippage); both configurable per strategy.
- Outcomes: ALL_FILLED, LEG1_PARTIAL, LEG1_FILLED_LEG2_FAILED,
  LEG1_LEG2_FILLED_LEG3_FAILED, PARTIAL_CYCLE, TIMEOUT, EXPIRED, REJECTED.
  Any non-complete outcome computes intermediate exposure (what asset is
  now held, its mark value, unwind estimate) and posts it to the
  portfolio — never hidden behind a bare "failed".
- Every adverse effect lands in P&L: fees, slippage vs expectation, dust,
  failed-leg exposure mark-to-market.
- TTL discipline: before simulating, revalidate all three books
  (version/health/age); material movement ⇒ recalculate; expired ⇒ reject
  (queue position never excuses staleness).
- Capital: reserve → simulate → settle/release exactly once via
  `internal/reservation` idempotency keys.

Tests: one deterministic scenario per outcome; conservation property
(balances + exposure + pnl always reconcile); race tests for concurrent
cycles; replay determinism test (same recording+config+seed ⇒ identical
cycle results).
