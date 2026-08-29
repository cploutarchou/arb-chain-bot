# arbitragescanner.io parity — feature matrix and the real remaining gap

Status: ANALYSIS 2026-08-29. Owner: product manager. Scope: answers
"what is still missing to replicate arbitragescanner.io, plus real
execution" against the tree at `40efca5`.

**Sourcing.** `arbitragescanner.io` is egress-blocked from this
development environment, so nothing here was read from the rendered site
directly. Competitor facts below come from search-engine summaries
quoting their `/`, `/about-dex-scanner` and `/plans` pages plus
independent reviews, retrieved 2026-08-29 — treat every competitor claim
in this document as **UNVERIFIED-INDIRECT** and re-read the pages in a
browser before any of it reaches marketing copy or a pricing decision.
The earlier pricing snapshot in `docs/design/packages.md` §1 was taken in
a session with site access and is the better source for their tiers.

Facts about **our own tree** are verified directly against `40efca5`.

## 0. The one structural fact about the competitor

**arbitragescanner.io does not execute trades.** It takes no API keys and
touches no funds; it emits signals and the trader acts manually. This is
stated in their own positioning and confirmed by independent reviews
(2026-08-29).

Two consequences for us:

1. "Replicate arbitragescanner" is a **breadth** problem — more venues,
   more strategy families, more chains. It is not an execution problem.
2. "…with real execution" is a **second, larger product** that they
   deliberately do not ship, because execution is where the capital risk
   and the regulatory exposure live. It stays behind the production
   execution gate (`crypto-arb-platform-command.md` §RULES). Our automatic
   PAPER execution is already ahead of them and is the evidence engine
   they have no equivalent of.

## 1. Parity matrix

| Capability | Them | Us at `40efca5` | Gap |
|---|---|---|---|
| CEX venues | 75+ claimed | 15 coded, soaked and enabled by default (`internal/screener/venue`) | T-075 IN_PROGRESS — Upbit, Bithumb, LBank, Phemex remain |
| Cross-venue spot spreads | yes | yes (T-067, net of both taker fees, identity + liquidity guard) | **parity** |
| Perp basis / funding | yes | yes (T-067 basis, carry, funding harvest) | **parity** |
| Futures↔futures | yes | modelled in `strategy-models.md`, not a shipped lane | small |
| Alerts → Telegram/e-mail/web | yes | yes (T-070, lifetime/cooldown/dedup) | **parity** |
| Saved scanners / templates | yes ("scanners" are their unit of pricing) | yes (screener settings, versioned) | **parity** |
| Packages / billing / affiliate | yes | yes (Paddle sandbox, T-081..T-088 partially landed) | **parity** |
| Public B2B API | yes | `internal/apikey` + package limit exists | verify surface |
| **DEX scanning** | **500+ DEX, 90+ chains; CEX↔DEX and DEX↔DEX** | **none — zero code; T-076 is a one-line TODO** | **the largest gap; see §2** |
| **P2P / fiat arbitrage** | **Binance/Bybit/HTX P2P, 7+ fiat currencies** | **none, and absent from the command** | **see §3** |
| On-chain address analysis | yes (their Enterprise tier) | none | out of scope — see §4 |
| Mass search | yes | partial (screener filters) | small |
| AI analysis | yes | `internal/ai` exists, not wired to the screener | small |
| **Automatic paper execution** | **none** | **yes (T-071), per-strategy ledger** | **we are ahead** |
| **Deterministic replay / campaigns** | **none** | **yes (`internal/backtest`, `cmd/campaign`)** | **we are ahead** |
| **Sequence-validated order books** | **none (aggregated quotes)** | **yes, chaos-tested** | **we are ahead** |
| Live execution | none | disabled by design | gated, not missing |

## 2. DEX is the real gap — and it is currently over-sold

`internal/entitlements/packages.go` ships `DexEnabled: true` and the
screener tier `"dex"` in the **Desk** and **Enterprise** packages, and
`docs/design/packages.md` §2 advertises "all CEX + DEX aggregators" at
those tiers. No DEX collector, quote source, chain client or venue
constant exists anywhere in `internal/`. Nothing reads `DexEnabled`.

Billing is still in Paddle **sandbox**, so nothing has been mis-sold to a
paying customer yet. It must not reach production in this state: either
the tier is built (Phase 28) or the entitlement is switched off until it
is. Filed as **T-102** (P0, product-correctness).

The plan already carries **T-076** ("DEX quotes via public aggregator
APIs … with gas cost model", TODO) — one line, no design, no
decomposition, and the only Phase 23 item that nothing has started. What
was missing is the design and the breakdown, now supplied:
`docs/design/dex-arbitrage.md`, decomposing T-076 into **T-110..T-116**.

The approach chosen there — aggregator **quote APIs** (1inch / 0x /
Jupiter / OpenOcean class) rather than direct pool math, signals + paper
only, no wallet keys, no contract deployment, no mempool interaction —
matches both our public-data-only posture and what the competitor actually
does (their Platinum tier is explicitly "~200 DEX **via aggregator**").

## 3. P2P — recommended NOT to build, and why

P2P arbitrage is the one competitor feature we should decline on the
merits rather than schedule.

- **It cannot be paper-validated.** There is no order book and no fill
  model — a P2P "price" is an advertisement, not an executable quote.
  Every other lane on this platform earns its claims from a campaign
  report; P2P structurally cannot, which breaks the rule that no strategy
  is described as profitable without evidence in `docs/campaigns/`.
- **Settlement is human and bank-side.** Fiat transfer confirmation,
  chargebacks, and counterparty fraud are outside anything code can
  manage.
- **It is jurisdiction-specific regulated activity.** The fiat corridors
  the competitor lists (RUB, UAH, TRY, KZT, …) carry sanctions and
  money-transmission exposure that varies by operator residence.

If it is built anyway, it ships as a **read-only ad-board monitor** with
no execution, no paper ledger and an explicit risk disclosure — and only
after an operator decision record under `docs/decisions/` covering the
fiat corridors offered, per the existing decision convention. Filed as
**T-103** (P3, blocked on that record).

## 4. Deliberately out of scope

- **On-chain address / wallet analysis.** Already declared out of scope in
  `packages.md` §1. It is a blockchain-analytics product (indexing,
  labelling, clustering), not an arbitrage product, and shares no
  machinery with anything here.
- **Copy trading, directional and statistical strategies.** Excluded by
  the platform charter and unchanged.

## 5. What "plus real execution" actually requires

Not a flag flip. `ErrLiveTradingDisabled` is unconditional by design
(`internal/execution/executor.go:140`), and **T-095 already states that no
work starts before gate conditions (a)–(c) exist** — so this section
scopes the work rather than filing a task for it, which would contradict
that rule.

What T-095 will require beyond the gate itself is an order-management
layer that does not exist anywhere in the tree today: idempotent client
order IDs, an order state machine, balance reconciliation against venue
truth on every restart, partial-fill recovery for the case where leg 2 of
3 fills and leg 3 rejects, and per-venue kill switches. That is a
subsystem on the scale of the screener, not an adapter — worth stating
plainly so "add real execution" is never mistaken for a small change.

Standing evidence position, unchanged: every measurement so far is
negative (triangular on Binance, best gross +4 bps against ~40 bps
costs). Breadth is worth building because it widens the search; it is not
itself evidence that a profitable lane exists.
