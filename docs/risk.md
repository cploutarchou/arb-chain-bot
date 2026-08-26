# Risk Engine Design

Status: DESIGNED (Phase 1). Implemented by `internal/risk` +
`internal/reservation`. The risk engine is deterministic: same inputs +
same config version ⇒ same decision. AI never overrides it; interfaces
never bypass it.

## 1. Position in the pipeline

Two gates:
1. **Qualification gate** (hot path): every candidate opportunity passes
   `Evaluate(op, RiskContext)` before it may be published as QUALIFIED.
2. **Execution gate** (paper engine): re-checked at reservation +
   revalidation time (books moved, capital changed, breakers tripped).

`RiskContext` snapshot: config version, book healths/ages, clock quality,
feed latencies, capital state (available/reserved/utilization), daily P&L,
drawdown, concurrent simulations, breaker states.

## 2. Limits (configurable, versioned)

Global, and overridable per-exchange / per-starting-asset / per-triangle
(most-specific wins; every effective value cited in decisions):

- minimum_net_edge_bps, minimum_expected_profit
- maximum_trade_size, maximum_capital_per_triangle,
  maximum_capital_utilization
- maximum_concurrent_simulations
- maximum_book_age_ms, maximum_book_age_spread_ms,
  maximum_data_latency_ms, maximum_execution_latency_ms
- maximum_slippage_bps, maximum_price_impact_bps
- maximum_daily_simulated_loss, maximum_drawdown
- minimum_data_quality_score
- opportunity_ttl_ms, latency_buffer_bps, risk_buffer_bps (consumed by
  pricing but owned by risk config)

Every rejection carries a machine-readable reason code
(`RISK_MIN_EDGE`, `RISK_BOOK_AGE`, `RISK_CAPITAL_UTIL`, …) that flows to
the opportunity record, metrics, and the console's rejection explorer.

## 3. Circuit breakers

Breakers subscribe to events and gate qualification (and optionally the
paper engine). States: CLOSED → OPEN → HALF_OPEN (probing) → CLOSED.
Triggers (SKILL.md §26): exchange disconnect, unstable WS (reconnect
storm), order-book sequence gap, REST reconciliation mismatch, clock
drift, abnormal latency, large unexpected slippage (measured vs modeled),
missing fee/instrument metadata, degraded persistence, memory pressure,
internal queue saturation, simulation inconsistency (invariant breach),
risk-service failure itself.

Policy per breaker: scope (global/exchange/market/triangle), open
condition (threshold + window), half-open probe, close condition, and
severity of the emitted alert. Safe default on any uncertainty: OPEN =
qualification paused = DO NOTHING. Breaker transitions are risk_events
(insert-only) and notifications.

## 4. Capital reservation

`internal/reservation` guards virtual capital with atomic semantics:

- States per asset: available / reserved / in-flight / settled.
- `Reserve(idempotency_key, req)`: single mutex-guarded (later sharded if
  measured) transition; duplicate keys return the original reservation;
  insufficient funds or conflicting-triangle locks reject deterministically.
- Conflict policy: triangles sharing a market+side may be serialized to
  avoid simulating consumption of the same displayed depth twice;
  configurable.
- Invariants (checked on every transition, violation = simulation
  inconsistency breaker + CRITICAL alert):
  `available + reserved + in_flight == session_total ± settled_pnl`,
  never negative, reservation settle/release exactly once.
- Race-tested: `-race` suite + property tests with concurrent
  reserve/settle/release storms.

## 5. Drawdown & loss controls

Daily simulated loss and max drawdown computed from the portfolio's
realized + mark-to-market P&L per session. Crossing a limit trips a
breaker scoped per config (usually global paper engine pause) and
requires operator acknowledgement (web/Telegram, audited) to resume —
automatic resume is off by default.

## 6. Interaction with AI

AI may RECOMMEND limit changes (with evidence/confidence/expiry). A
recommendation becomes config only through the human approval flow
(RBAC-gated, audited, versioned). There is no code path from
`internal/ai` to `internal/risk` state. Risk config changes made by any
actor emit ConfigChanged + audit + notification.

## 7. Observability

Metrics: rejections by reason code, breaker states, capital utilization,
drawdown, daily P&L, limit-evaluation latency. Console Risk Center reads:
current limits (with effective overrides), breaker board, active blocks,
risk-event timeline, and why-blocked explanations per opportunity id.

## 8. Test matrix

- Table-driven: every limit's pass/fail boundary at exact decimal edges.
- Property: monotonicity (worsening an input never turns REJECTED into
  QUALIFIED under same config).
- Race: reservation storms; breaker transitions under concurrent events.
- Chaos: each trigger's fault injection proves qualification stops within
  one evaluation cycle and resumes only per policy.
- Replay: identical decisions across replays at fixed config+seed.
