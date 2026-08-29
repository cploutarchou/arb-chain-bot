# DEX arbitrage — scope, quote model, costs, identity, paper execution

Status: DESIGNED 2026-08-29. Owner: DEX engineer. Decomposes **T-076**
(Phase 23) into T-110..T-116.
Closes the gap recorded in `docs/design/arbitragescanner-parity.md` §2.
Conventions (decimal-only money math, executable sides, data-age gates,
fee placement, fill and latency model) are inherited verbatim from
`docs/design/strategy-models.md` §0–§1 and are not restated.

Nothing in this document is a result or a guarantee. DEX lanes earn
claims the same way every other lane does: a campaign report in
`docs/campaigns/`.

## 1. Scope decision

**Build:** aggregator-quoted DEX pricing for two lanes — CEX↔DEX and
DEX↔DEX on the same chain — as **signals plus automatic paper execution**,
using the same `Quote` seam, alert rules and paper ledger as the existing
screener lanes.

**Refuse, in this phase:**

| Refused | Why |
|---|---|
| Direct pool math (V2 `x·y=k`, V3 tick traversal, Curve invariants) | An aggregator quote already internalises routing, split fills and price impact at the **actual size**. Reimplementing per-AMM curve math multiplies the surface where a decimal error becomes a phantom spread. |
| Wallet keys, signing, transaction submission | Nothing in this platform holds a key that can move funds. A hot wallet is a strictly worse credential than an exchange key: no withdrawal allowlist, no venue-side fraud controls, irreversible. |
| Arbitrage contracts / flash swaps | Requires deployment, audit and MEV competition. Out of proportion to a signals product. |
| Mempool / private-relay integration | Only meaningful when submitting real transactions, which the gate forbids. |
| Cross-chain bridging | Bridge latency (minutes) and bridge risk dominate any spread; bridges are the single largest loss category in DeFi. Cross-chain pairs are **displayed as informational only**, never as an executable lane. |

This mirrors what the competitor actually sells — their DEX tier is
explicitly "via aggregator" — and keeps our public-data-only posture.

## 2. Token identity is the correctness core

The screener already learned this on CEXes: T-067 added an asset-identity
guard after live VON / TROLL / XTER symbol collisions produced fake
spreads. **On DEXes the problem is categorically worse**, because anyone
can deploy a token called `USDC` for the price of gas, and honeypot tokens
are deployed specifically to appear in scanners.

Binding rules:

- A DEX asset is keyed by **`(chain_id, contract_address)`**, address
  lowercased and checksum-validated. A symbol is a display label and is
  **never** a join key. Any code path that matches DEX assets by symbol is
  a P0 defect, the same severity as `float64` in a money path.
- A pair enters the scanning universe only if both legs' contract
  addresses appear on a **canonical token list** pinned per chain (source
  URL + retrieval date recorded, refreshed on a schedule, diffs logged).
  Unlisted addresses are excluded, never "probably fine".
- For a CEX↔DEX lane the CEX leg must be the **same contract on the same
  chain**. The venue's own currency/network endpoint supplies its deposit
  contract address and chain name; the pair is skipped with reason
  `IDENTITY_UNKNOWN` when the venue does not publish it. Matching by
  ticker across a CEX and a chain is the exact bug class that makes a
  scanner emit spreads that cannot be traded.
- Fee-on-transfer, rebasing and blacklistable tokens break the "amount
  out equals amount received" assumption. Detect via the token list's
  flags where available; otherwise exclude tokens whose quoted output and
  simulated output disagree beyond tolerance. Reason `TOKEN_SEMANTICS`.

## 3. Quote sources

One `QuoteSource` interface, one implementation per aggregator, mirroring
`internal/screener/venue`'s collector + conformance-test pattern.

```
Quote(ctx, chainID, tokenIn, tokenOut, amountIn) →
    (amountOut, estGasUnits, route, ts)
```

Candidates (each to be verified in T-110 before use): 1inch, 0x Swap API,
OpenOcean, ParaSwap for EVM chains; Jupiter for Solana. Selection criteria
are public reachability, a documented rate limit, quote-at-size support,
and gas estimation in the response.

**The size trap.** An AMM price is a function of trade size. A quote taken
at a nominal 1 unit and applied to a real size manufactures a spread that
does not exist. Every quote is requested **at the size the rule intends to
trade**, and a rule that sweeps sizes issues one quote per size. Caching a
quote across sizes is a P0 defect.

**Credential class.** Aggregator API keys are *data-source* keys: they
read prices and cannot move funds. They are a new vault group, distinct
from the exchange credential group, and `secrets.IsConsumable` stays false
for exchange credentials exactly as today. A DEX quote request is never
signed with an exchange key.

## 4. Cost model

Net edge for a DEX leg differs from a CEX leg in one structural way: **gas
is a fixed cost per transaction, not a rate.** That single fact sets a
minimum viable size and must be first-class, not a footnote.

```
gas_quote   = gas_units × gas_price_native × price(native → quote)
net_out     = amount_out_quoted × (1 − pool_fee_already_in_quote)   # aggregator quote is net of pool fees
leg_cost    = gas_quote                                            # plus CEX-side taker fee on the other leg
min_notional_for_gas = gas_quote / edge_frac
```

Worked example (illustrative inputs, not a measurement): a 150 000-gas
EVM swap at 20 gwei with the native asset at 3 000 quote costs
`150000 × 20e-9 × 3000 = 9.00` quote. Against a 30 bps gross edge, break-even
notional is `9.00 / 0.0030 = 3 000` quote — **below that size the lane is
structurally unprofitable regardless of the spread.** Rules therefore carry
a `min_notional` derived from live gas, not a constant, and a quote whose
edge cannot clear `gas_quote` is skipped with reason `GAS`.

Additional costs, all modelled explicitly, none defaulted to zero:

- CEX taker fee on the CEX leg (existing `fees.PlacementFor`).
- CEX **withdrawal fee** and network fee when inventory moves, amortised
  per the cross-venue inventory model in `strategy-models.md` — CEX↔DEX is
  an inventory lane, not a round trip (§5).
- Aggregator/router fee where the source charges one.
- Slippage tolerance: the quote's `amountOut` is a quote, not a fill.
  Model as `amountOut × (1 − slip_frac)` with the same per-rule allowance
  the CEX lanes use, and measure realised deviation against the next poll.

## 5. Settlement latency makes CEX↔DEX an inventory lane

Block inclusion is seconds (chain-dependent), but the **CEX side of the
round trip is not**: moving a token from an exchange to a wallet requires
a withdrawal, its confirmation count, and often a manual-review window —
minutes to hours. There is no fast there-and-back.

Therefore CEX↔DEX is funded like cross-venue spot: **inventory
pre-positioned on both sides**, trades executed against standing balances,
and periodic rebalancing whose amortised cost is charged to the lane. The
existing `NetworkStatus` (`open`/`closed`/`unknown`) already gates this —
a lane whose token cannot currently be withdrawn on the required chain is
skipped with reason `NETWORK_CLOSED`, and `unknown` is treated as closed,
never as open.

Data-age gates apply per §1.1 of `strategy-models.md`, with one addition:
a DEX quote carries the **block number** it was computed against, and a
quote more than `N` blocks stale (per-chain, default 2) is refused.

## 6. MEV — why paper is honest and live would not be

A real swap broadcast to a public mempool can be sandwiched: the realised
output is worse than the quote, systematically and adversarially. Paper
execution does not experience this, so **every DEX paper report carries a
standing caveat that its fills exclude MEV extraction**, in the same spirit
as the existing "self-impact is not modelled" caveat in
`docs/deployment.md`.

This is recorded as a named blocker on the production execution gate for
DEX lanes specifically: paper evidence from a DEX lane is *not* sufficient
to justify live DEX execution, because the paper model omits the dominant
adversarial cost. Live DEX execution would additionally require private
transaction submission and a re-validated cost model.

## 7. Package seam

DEX enters through the existing screener types rather than a parallel
stack:

- `screener.Venue` gains DEX venue constants (`VenueUniswapV3`,
  `VenuePancakeV3`, `VenueJupiter`, …) plus a `VenueKind` discriminator
  (`cex` | `dex`) so lane code branches on kind, never on a name string —
  the same rule as `exchange.Capabilities`.
- `internal/screener/dex` holds quote sources, token lists, gas oracles
  and chain metadata; `internal/screener/venue` is untouched.
- Normalised `Quote` remains the only contract into spreads/alerts/paper.
  A DEX quote fills the same struct with `bid`/`ask` derived from
  `amountOut` at the requested size, plus DEX-only fields carried
  alongside (chain, block, gas estimate, route hash).
- Entitlement: the `"dex"` screener tier and `DexEnabled` become real and
  enforced here (T-102 keeps them switched off until then).

## 8. Verification status

Everything in §3 (which aggregators, their limits, their key policy) is
**UNVERIFIED** — the aggregator documentation hosts were not reachable
from this environment. T-110 is the research round that verifies each
source against its current official docs with URL and access date, exactly
as `docs/research/screener-endpoints.md` does for CEX venues, before any
collector is written.

Per-chain facts to verify in the same round: block time, typical swap gas
units per router, native asset symbol, canonical token-list URL, and
whether the chain's finality makes a 2-block staleness gate sensible.

## 9. Tasks

- **T-110** DEX research round → `docs/research/dex-endpoints.md`.
- **T-111** `QuoteSource` interface + first aggregator + conformance test.
- **T-112** Token identity: canonical lists, `(chain_id, address)` keying,
  CEX contract-address matching, honeypot/fee-on-transfer exclusion.
- **T-113** Gas oracle + cost model + `min_notional` derivation.
- **T-114** CEX↔DEX lane (inventory model, `NetworkStatus` gating).
- **T-115** DEX↔DEX same-chain lane.
- **T-116** Paper execution, nightly report, MEV caveat, entitlement flip.
