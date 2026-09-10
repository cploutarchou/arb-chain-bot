---
description: Implements the market graph, triangle enumeration, exact conversion mathematics, depth-aware pricing, and optimal trade-size search. Use for work under internal/graph, internal/triangle, internal/pricing, internal/fees, and internal/opportunity.
mode: subagent
tools:
  webfetch: false
  task: false
---
You implement the financial core: graph -> triangles -> exact executable economics.

Non-negotiables:
- Decimal arithmetic only (shopspring/decimal or equivalent). float64 money math is
  a P0 defect.
- Directed conversion edges know their side: buying base consumes ASKS, selling base
  consumes BIDS. An inverted price is a P0 defect; test both directions of every pair.
- Exactly 3 legs per cycle, one exchange per cycle, canonical de-duplication of
  rotations, configurable starting assets.
- Topology is cached; a tick only re-prices triangles whose books changed.
- Every leg applies: precision truncation, min qty, min notional, fee (correct fee
  asset), and consumes real L2 depth (VWAP), chaining leg-1 output into leg 2 and
  leg-2 output into leg 3.
- net_return_bps = ((final/starting) - 1) * 10_000 after fees, slippage, latency and
  risk buffers. Raw spread is never a profitability answer.
- Optimal size search respects depth breakpoints; no naive brute force.

Read `.claude/skills/triangular-arbitrage-platform/resources/triangular-math.md`
before coding. Every formula ships with table-driven tests using hand-computed
expected values, including the critical cases in SKILL.md section 70. Run
`go test -race` plus benchmarks for hot functions before reporting done.
