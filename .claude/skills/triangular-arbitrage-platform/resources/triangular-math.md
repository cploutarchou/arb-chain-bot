# Resource: Triangular Mathematics

Authoritative background: `docs/research/triangular-constraints.md`.
Implementation home: `internal/graph`, `internal/pricing`, `internal/fees`.

## Directed edges

A market BASE/QUOTE yields two directed conversion edges:
- QUOTE→BASE ("buy base"): consumes **ASKS**; out_base = f(in_quote walking
  ask levels); price worsens upward.
- BASE→QUOTE ("sell base"): consumes **BIDS**; out_quote = f(in_base
  walking bid levels); price worsens downward.
Never invert a price to fake the other direction — the other side of the
book is a different liquidity curve. Test BOTH directions of every pair.

## Cycle evaluation (exact, decimal)

Given starting amount A0 in asset S and legs L1,L2,L3:
for each leg i: in_i → depth-walk VWAP fill → gross_out_i →
fee applied in the venue's fee asset (received-asset fees reduce the
quantity entering the next leg; quote-side fees reduce final proceeds) →
precision truncation to the instrument's step/decimals → out_i.
A3 = out_3 (must be asset S).

gross_return_bps = (A3_before_buffers / A0 - 1) * 10_000
net = A3 - A0 - latency_buffer - risk_buffer (buffers configured in bps of
A0, converted exactly). net_return_bps analogous. Raw top-of-book spread
is never a profitability answer.

## Depth walk

Consume levels in order; each level contributes min(remaining, level_qty)
at level price; track depth_used per leg and price_impact vs best level.
Leg chaining: out_1 is in_2; out_2 is in_3. If depth exhausts before the
target size, the leg caps the cycle (liquidity_limit).

## Optimal size

Profit(size) is piecewise concave-ish with breakpoints at level boundaries
of all three legs. Search: enumerate candidate sizes at mapped breakpoints
(project each leg's cumulative-depth boundaries back to starting-asset
units through the chain) + bounded refinement between the best adjacent
candidates. No naive brute-force grids. Respect min_notional (floor) and
depth (ceiling); floor > ceiling ⇒ triangle structurally untradeable at
this instant.

## Quantization

Apply the venue's model exactly: step/tick (Binance LOT_SIZE stepSize,
PRICE_FILTER tickSize; NOTIONAL minNotional) or decimals (Bitget/Gate).
Truncate quantities DOWN; validate min qty and min notional per leg AFTER
truncation. Dust from truncation is a real cost — it stays in the
intermediate asset and is accounted in P&L.

## Canonical triangles

A triangle is an unordered market set with a starting asset; rotations are
the same triangle unless a different starting asset is configured
(USDT→BTC→ETH→USDT ≡ BTC→ETH→USDT→BTC only as rotation; with starting
asset fixed to USDT, only the USDT rotation exists). Canonical key: sorted
market ids + starting asset. Reject self-loops, duplicate markets,
disabled/untradeable markets, markets without required books.

## Required tests (SKILL.md §70)

Hand-computed table-driven cases for: bid/ask inversion, decimal rounding,
fee in base vs quote vs BNB-style, min qty/min notional boundaries,
precision truncation, insufficient/zero depth, profitable-at-top-of-book
but not after depth/fees/slippage, and both-directions edge pricing.
