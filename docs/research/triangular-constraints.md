# Triangular Arbitrage — Structural Constraints Analysis

Status: ANALYSIS (domain knowledge, not time-sensitive web research)
Last updated: 2026-08-26
Owner: quant research

This document explains the economic and microstructure constraints that bound
what a single-exchange triangular-arbitrage system can achieve. It exists so
that every other design decision (exchange choice, latency budget, fee
modeling, risk buffers, and honest reporting) is made against reality rather
than against the raw-spread illusion.

There is no guaranteed profit anywhere in this document or this platform.

---

## 1. What the opportunity actually is

A triangular cycle converts a starting asset through two intermediate assets
back to itself on ONE exchange:

```
USDT -> BTC   (buy BTC/USDT, consumes asks)
BTC  -> ETH   (buy ETH/BTC,  consumes asks)
ETH  -> USDT  (sell ETH/USDT, consumes bids)
```

A cycle is profitable only when the product of the three *executable*
conversion rates, after fees, exceeds 1 by more than the total cost of
uncertainty (slippage, latency movement, failure risk):

```
r_cycle = r1_exec * (1 - f1) * r2_exec * (1 - f2) * r3_exec * (1 - f3)
net_return_bps = (r_cycle - 1) * 10_000 - latency_buffer_bps - risk_buffer_bps
```

Where each `r_exec` is the depth-aware VWAP rate for the actual quantity
traded, in the correct direction (bid vs ask), after precision truncation.
Any analysis using mid prices or top-of-book quotes for all three legs
overstates the edge, usually decisively.

## 2. Why raw triangular deviations exist at all

- Order flow is asymmetric per market: a burst of buying in BTC/USDT moves
  that book milliseconds before ETH/BTC and ETH/USDT adjust.
- Market makers quote each pair independently with inventory constraints;
  cross-pair consistency is enforced only by arbitrageurs.
- Volatility spikes, listings, depegs, and news create transient
  inconsistencies larger than quoting noise.

The same forces that create deviations also close them: professional firms
run the identical computation with co-located, sub-millisecond
infrastructure. On major venues the *persistent* cross-rate deviation is held
inside the arbitrageurs' cost boundary almost continuously.

**Consequence:** the observable steady-state deviation on liquid triangles is
typically smaller than round-trip taker fees. Positive *net* opportunities at
retail latency are rare, small, short-lived, and concentrated in stressed or
less-liquid moments. The platform's purpose is to *measure* this honestly,
not to assume it away.

## 3. The fee wall (dominant constraint)

Three legs mean three fees. At a base-tier taker fee of `t` bps, the fee
floor is `~3t` bps before any other cost:

| taker fee per leg | 3-leg fee floor |
|---|---|
| 10 bps (0.10%) | ~30 bps |
| 8 bps (0.08%) | ~24 bps |
| 7.5 bps with discount | ~22.5 bps |
| 2.5 bps (high VIP) | ~7.5 bps |
| 0 bps (promo pairs) | 0 bps |

(Exact multiplicative form: `1 - (1-t)^3`, ≈ `3t` for small `t`.)

A triangle must therefore show a *gross* cross-rate deviation exceeding
~24–30 bps at base retail tiers before slippage and buffers — an
extraordinarily high bar on efficient books, where typical deviations are
0–10 bps. Implications:

1. Fee modeling must be exact, per pair, per account tier, including
   discounts and any zero-fee promotional pairs (verified in
   `fees.md`). A single wrong fee assumption invalidates every result.
2. Zero-fee or discounted pairs, where they exist, dominate triangle
   viability and must be first-class configuration, not an afterthought.
3. Fee-in-kind conventions change the math: a fee charged in the *received*
   asset reduces the quantity entering the next leg; a fee charged in a
   native token (e.g. exchange token balances) changes the P&L currency.
   The engine models where the fee is taken, not just its rate.

## 4. The latency constraint

The deviation that triggers detection is measured on books that are already
`network_latency + processing_latency` old, and execution adds another
round-trip per leg (sequential legs: three round-trips).

- Opportunity half-life on liquid pairs is typically well under a second;
  competitive closure happens in milliseconds.
- From a non-colocated vantage point (tens of ms RTT), a large fraction of
  detected opportunities will be gone or inverted by leg 2 or leg 3.
- Therefore: (a) book age and feed latency are first-class health metrics
  with hard qualification caps; (b) the simulator prices fills on the book
  *as of fill time*, not detection time; (c) a configurable
  `latency_buffer_bps` is subtracted from every opportunity before
  qualification; (d) measured fill-vs-detect slippage from paper cycles is
  the platform's most important self-calibration signal.

## 5. The depth constraint

Top-of-book quantity on most pairs is small relative to meaningful capital.
Executable size is bounded by the *minimum* effective depth across the three
legs after conversion chaining:

- Leg sizing must walk L2 levels (VWAP), and the output quantity of leg 1
  (post-fee, post-truncation) is the input of leg 2, and so on.
- Profit is not monotonic in size: edge decays with depth consumption, so
  there is an optimal size; beyond it, marginal fills lose money. The
  optimal-size search (breakpoint-aware, not brute force) is core math.
- Depth displayed is not depth guaranteed: quotes are canceled and consumed
  adversarially. The simulator must support depth-haircut assumptions
  (e.g. only x% of visible depth is achievable) as a stress parameter.

## 6. Precision, lot-size, and notional constraints

Every leg quantizes: quantity step, price tick, minimum quantity, minimum
notional. Effects:

- Truncation at each leg leaves dust and reduces the compounding quantity;
  across three legs this alone can consume several bps on small sizes.
- Minimum notional per leg sets a *floor* on trade size, while depth sets a
  *ceiling*; some triangles have floor > ceiling and are permanently
  untradeable at acceptable cost — they must be rejected structurally, not
  rediscovered every tick.
- All arithmetic is decimal fixed-point; float64 rounding is disqualifying
  for money paths.

## 7. Execution/leg risk (the asymmetric loss)

The strategy's loss profile is dominated not by "edge was slightly smaller"
but by *incomplete cycles*: leg 1 fills, leg 2 rejects or the price runs
away, and the portfolio is left holding an unintended intermediate asset in
a moving market. Constraints:

- The probability of leg failure rises exactly when opportunities appear
  (volatility, thin books, feed stress) — adverse selection is structural.
- Failed-cycle handling must be modeled (unwind cost) and reported as its own
  P&L line; a strategy can be positive on completed cycles and negative
  overall.
- IOC-style limit semantics bound slippage per leg at the cost of higher
  failure probability; market orders raise completion probability at the
  cost of unbounded slippage. Both must be simulatable; the trade-off is an
  empirical question per exchange and triangle.

## 8. Data-quality constraint

A sequence gap, a stale book, or clock drift does not merely add noise — it
manufactures *false opportunities* (a frozen leg looks mispriced against two
live legs). Hence:

- Only HEALTHY books qualify opportunities; STALE/CORRUPTED books suppress
  the affected triangles entirely.
- Cross-book synchronization matters: three healthy-but-differently-aged
  books can still fabricate a phantom edge; the qualification gate caps the
  *maximum* book age and the *spread* of ages across the three legs.
- Clock discipline (NTP + exchange server-time offset) is required for every
  latency measurement to mean anything.

## 9. Competitive reality and honest expectations

- On tier-1 venues, firms with maker-based triangular strategies (earning
  rather than paying fees, at microsecond latency) define the equilibrium.
  A taker-based retail-latency system is structurally behind them.
- Realistic outcomes for this platform's paper trading on liquid triangles:
  most detected raw deviations will be rejected by the fee/slippage/latency
  gates; qualified opportunities will be infrequent and concentrated in
  volatile intervals and in less crowded triangles; measured net edge may
  well be negative. That is a *valid research result*, not a failure of the
  platform. The platform must never massage assumptions until results look
  positive.
- Stress testing in the *pessimistic* direction (higher fees, worse fills,
  added latency, depth haircuts) is mandatory before any conclusion. A
  configuration profitable only under perfect fills is flagged as worthless
  by definition (SKILL.md section 80).

## 10. Constraints the platform imposes on itself

- One exchange per cycle; exactly three legs; no cross-exchange legs.
- Paper/replay/simulation only; `LiveExecutor` permanently returns
  `ErrLiveTradingDisabled`.
- Deterministic risk engine gates every qualification; AI advises, humans
  approve, nothing bypasses the gate.
- Reject on uncertainty: if any critical input (book health, fee metadata,
  instrument rules, clock quality) is unknown, the opportunity is rejected.
  Correct rejection is cheaper than false confidence.

## 11. What follows for platform selection

The exchange comparison (`exchanges.md`, `fees.md`, `market-data.md`) should
weight, in roughly this order:

1. L2 feed correctness and reconstructability (sequence rules, checksums,
   snapshot discipline) — without this nothing downstream is trustworthy.
2. Effective 3-leg fee floor including discounts/promos — it defines the
   qualification bar.
3. Pair count and stablecoin-market breadth — it defines the triangle
   universe size.
4. Spot testnet/demo quality — it defines safe development velocity.
5. Rate limits and WS stability — they define resync robustness.
6. Headline volume — relevant only through depth actually visible on books.

Final scoring and the first/second exchange recommendation live in
`final-platform-selection.md`.
