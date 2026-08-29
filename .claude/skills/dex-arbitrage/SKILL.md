---
name: dex-arbitrage
description: Workflow and non-negotiables for DEX lanes — aggregator-quoted on-chain pricing, token identity by (chain_id, contract_address), gas-aware cost modelling, CEX↔DEX inventory lanes and DEX↔DEX same-chain lanes, as signals plus automatic PAPER execution. Use for any work under internal/screener/dex, DEX quote sources, token lists, gas oracles or DEX lane math.
when_to_use: "dex", "uniswap", "pancakeswap", "jupiter", "aggregator quote", "on-chain", "gas", "token list", "cex-dex", "dex-dex", "chain", "swap"
allowed-tools: Read Grep Glob Write Edit Bash Agent WebFetch WebSearch
argument-hint: [task id or feature]
---

# DEX arbitrage workflow

Task: $ARGUMENTS

Design of record: `docs/design/dex-arbitrage.md` (read it in full — §1
scope, §2 identity, §4 costs are binding). Gap analysis:
`docs/design/arbitragescanner-parity.md` §2. Plan: MASTER_PLAN T-076
decomposed into T-110..T-116. Endpoint facts:
`docs/research/dex-endpoints.md` — use
ONLY fields marked VERIFIED; anything UNVERIFIED is re-verified against
the source's current official docs (URL + access date) before use.

Inherit every convention from `.claude/skills/scanner-suite/SKILL.md`;
this skill adds what is specific to on-chain venues.

## Non-negotiables

- **Identity is `(chain_id, contract_address)`, never a symbol.** Address
  lowercased, checksum-validated, present on a pinned canonical token
  list. Matching DEX assets by ticker — or matching a CEX asset to a DEX
  asset by ticker — is a P0 defect, same severity as `float64` in a money
  path. This is the VON/TROLL/XTER failure (T-067) with a lower barrier
  to entry: anyone can deploy a token named `USDC`.
- **Quote at the size you intend to trade.** An AMM price is
  size-dependent. Reusing a quote across sizes, or scaling a small quote
  up, manufactures spreads that cannot be filled. One quote per (pair,
  size).
- **Gas is a fixed cost, not a rate.** Every net calculation subtracts
  `gas_units × gas_price × price(native→quote)` and every rule derives
  `min_notional` from live gas. A lane whose edge cannot clear gas is
  skipped with reason `GAS`, never displayed as positive.
- **No keys that move funds.** No wallet private keys, no signing, no
  transaction submission, no contract deployment, no mempool or private
  relay. Aggregator API keys are a data-source vault group only;
  `secrets.IsConsumable` stays false for the exchange credential group.
- **`unknown` network status is treated as closed.** A CEX↔DEX lane whose
  token cannot be verified as withdrawable on the required chain is
  skipped (`NETWORK_CLOSED`), never assumed tradable.
- **Cross-chain pairs are informational only** — never an executable lane,
  never a paper trade. Bridge latency and bridge risk are not modelled.
- **Every DEX paper report carries the MEV caveat** (design §6): paper
  fills exclude sandwich extraction, so DEX paper evidence alone can never
  satisfy the production execution gate for a DEX lane.
- Money math is decimal end to end. Never edit `internal/risk`,
  `internal/pricing` or `internal/simulation` arithmetic as a side effect.
- Every displayed number is net of fees **and gas**, carries a data age
  and a block number, and nothing is described as guaranteed or risk-free.

## Steps

1. Read `docs/design/dex-arbitrage.md` for the seam you are touching. The
   normalised `screener.Quote` stays the only contract into spreads,
   alerts and paper execution — DEX-specific fields ride alongside it.
2. Branch on `VenueKind` (`cex` | `dex`), never on a venue name string.
3. New quote source → implement `QuoteSource`, add recorded fixtures, and
   make it pass the shared conformance test before wiring it to a lane.
   Per-source rate gate honouring the documented limit and `Retry-After`,
   modelled on `internal/screener/venue/ratelimit.go`.
4. Table-driven tests with golden vectors for every cost and identity
   rule; a fixture for each rejection reason (`IDENTITY_UNKNOWN`,
   `TOKEN_SEMANTICS`, `GAS`, `NETWORK_CLOSED`, stale block).
5. `go test -race` on packages touched; `gofmt`, `go vet`,
   `golangci-lint run ./...` clean before reporting done.
6. `/code-review` before any task moves to DONE. Statuses change only when
   acceptance criteria actually pass.

## Definition of done for a DEX lane

Collector soaked without rate-limit refusals; identity guard proven
against a deliberate symbol-collision fixture; cost model reproducing the
design's worked example; lane wired to alert rules and automatic paper
execution with its own ledger tag; a nightly report in `docs/campaigns/`
carrying the MEV and inventory caveats. No profitability claim without
that report.
