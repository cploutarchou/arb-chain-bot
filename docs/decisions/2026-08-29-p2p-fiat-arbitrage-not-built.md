# Decision: P2P / fiat arbitrage will not be built

- Date: 2026-08-29
- Decided by (operator, legal name and role): the repository operator
  (product owner). Legal name not recorded in the material this write-up was
  made from; the operator fills it in on signing.
- Counsel consulted (firm, person, date): none — **and none is required for
  this direction.** Declining to enter a regulated activity needs no legal
  opinion; entering one does. That asymmetry is the reason this record can
  be written now and its reversal cannot (see "Conditions").
- Jurisdictions considered: none analysed, deliberately. The competitor's
  corridors (RUB, UAH, TRY, KZT, AMD, UZS, KGS) each carry their own
  money-transmission and sanctions position, and analysing them is work that
  only a decision to build would justify.
- Licence / exemption relied on (per jurisdiction): none needed — unchanged
  signals-only SaaS posture (`docs/design/packages.md` §7). This decision
  removes a candidate activity rather than adding one.
- Scope: signals only. Unchanged: no package offers live execution,
  `execution.live` is `false` by construction, no exchange key is read by
  any component, and no fiat ever moves through this platform.
- Evidence reviewed (docs/campaigns/ report ids, dates):
  `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3/` (2026-08-27) — 0 cycles
  executed, no profitability statement. Cited here only to state that the
  evidence bar this platform holds every lane to is currently unmet by every
  lane, which is part of why adding a lane that **can never** meet it is the
  wrong direction.
- Security review reference: not applicable — nothing is being built.
- Insurance in place: not applicable.
- Decision: **P2P / fiat arbitrage is not built, and is not on the
  roadmap.** T-103 is closed as declined rather than left open. No
  implementation task exists, and none is to be created without a record
  superseding this one.
- Conditions and expiry: no expiry. Reversal requires a **new** record that
  supersedes this one and that supplies what this one deliberately does not:
  named counsel, the specific fiat corridors to be offered, and the
  money-transmission and sanctions analysis for each, per the operator's own
  residence. Any reversal also inherits the shape in §3 below.
- Supersedes: nothing. Supersedes no record; it closes the question
  `docs/design/arbitragescanner-parity.md` §3 raised.

*(Bullet block is `TEMPLATE.md` verbatim; gate-shaped fields are answered
rather than deleted so the file stays diffable against the template.)*

## 1. What was asked

The 2026-08-29 parity review found that arbitragescanner.io scans P2P ad
boards (Binance / Bybit / HTX P2P) across seven or more fiat currencies, and
that we have none of it — the platform command never mentions P2P at all.
Filed as **T-103**, blocked on this decision either way.

Competitor facts here are **UNVERIFIED-INDIRECT**: `arbitragescanner.io` is
egress-blocked from the development environment, so its feature list came
from search-engine summaries rather than the rendered site. That uncertainty
does not affect this decision — the reasons below hold whatever their exact
corridor list is.

## 2. Why not, on the merits

Three reasons, in the order that decides it.

**It cannot be paper-validated, so it can never earn a claim.** Every lane
on this platform earns its claims one way: automatic paper execution
producing a campaign report in `docs/campaigns/`. A P2P "price" is an
**advertisement**, not an executable quote. There is no order book, no
depth, no fill model, and no way to know whether a counterparty would have
traded. A P2P lane could therefore emit signals forever and never produce
evidence — which does not merely make it unmeasurable, it makes it the one
lane whose numbers we could never honestly describe. That breaks the rule
the whole programme rests on, and the rule is worth more than the lane.

**Settlement is human and bank-side.** Fiat transfer confirmation,
chargebacks, reversed payments and counterparty fraud sit entirely outside
anything this code can observe or manage. The platform's core competence —
measuring net-of-fee edges from public market data with decimal arithmetic
and data-age gates — buys nothing here. We would be shipping a scraper of
classified ads with a trading product's authority attached to it.

**It is jurisdiction-specific regulated activity, and the risk is
asymmetric.** The corridors involved carry money-transmission and sanctions
exposure that varies by the operator's residence and by the counterparties'.
Being wrong in the cautious direction costs a feature we have no evidence
anyone would pay for; being wrong in the permissive direction is a
regulatory problem, and one that a signals-only posture is specifically
designed to keep us out of.

## 3. What it would have to look like, if this is ever reversed

Recorded so a future reversal starts from the constraints rather than
rediscovering them:

- **Read-only ad-board monitor.** No execution, no paper ledger, no
  campaign report, and no place in the auto-paper strategy set.
- **Never described as arbitrage or as profitable.** The UI and copy call it
  what it is: advertised prices on a third-party board, with an explicit
  statement that they are not executable quotes and carry no fill model.
- **Its own risk disclosure**, distinct from the standing one, covering
  counterparty fraud and payment reversal.
- **Corridor allow-list, not a global feed** — each corridor enabled only
  where the superseding record's analysis covers it.
- It would still not satisfy, or contribute to, the production execution
  gate.

## 4. What this does not decide

This record covers P2P fiat corridors only. It says nothing about DEX
(designed, not built — `docs/design/dex-arbitrage.md`, T-110..T-116, and
withdrawn from every package by the 2026-08-29 record), nothing about
on-chain wallet analysis (already out of scope, `packages.md` §1), and
nothing about the production execution gate, which is unchanged and still
requires its own record under §RULES (c).
