---
title: Perpetuals basis and funding monitor
slug: /product/perpetuals
status: DRAFT — compliance-reviewer sign-off required before publish
drafted: 2026-08-27
owner: content-copywriter
evidence: docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3 (zero qualified cycles; not a perps report)
constraints: review-2026-08-27 items 4, 5, 6, 7, 8; strategy-models.md §0, §1.4, §8
gating: page shows a country-dependent "information only" banner; sign-up country and consumer status decide whether monitor rows are actionable in the console [COUNSEL]
---

<!-- Item 8: this page promotes nothing. It describes a monitor. The word
     The banned strategy verb does not appear. Funding rates are never shown as annualised
     figures in marketing copy. -->

## Banner (country-dependent)

**Default:** Perpetual futures are derivatives. In some countries they are
not available to retail clients. This page and the monitor are
information only; whether you can trade a perpetual depends on your
country and your venue, not on {{brand}}.

**UK retail variant [COUNSEL]:** [prescribed risk warning text].

## Hero

**Eyebrow:** Product · Perpetuals

**H1:** Basis and funding, side by side, predicted kept apart from settled.

**Sub:** For each perpetual on each enabled venue the monitor shows the
mark, the index, the spot bid and ask, the current funding rate, the
predicted next rate, the settlement time and the interval. Cross-venue
views line the same contract up across venues.

**CTA:** Open the monitor on the Operator trial

## What is measured

- **Basis.** Perpetual against spot on the same venue, and perpetual
  against perpetual across venues, always from executable sides when a
  simulated position is involved.
- **Funding.** The rate the venue publishes, per interval, exactly as
  published. The interval is re-read every poll because venues change it
  in volatile markets.
- **Predicted vs settled.** The predicted rate is labelled "predicted" and
  is used only for display and rule triggers. Only the settled rate, read
  from funding history after settlement, is ever booked to a simulated
  ledger.
- **Freshness.** Funding fields must be younger than two refresh
  intervals and the next settlement must be in the future, or the row is
  dropped with the reason shown.

## Rules you can build

- Basis threshold between spot and perpetual on one venue.
- Basis threshold between two venues' perpetuals on the same base.
- Funding-rate threshold, sign and persistence over N intervals.
- Each rule may attach paper execution (Operator and above for carry;
  Desk and above for futures-against-futures and funding positions).

## How the simulation treats a perpetual leg

One-times notional: the full notional is posted as simulated collateral,
so no leverage is modelled. A hard stop closes the simulated position at
a fixed adverse move. Funding is booked one row per settlement at the
settled rate. Unwind costs are included in the net figure. These are
modelling choices; a real account on a real venue behaves differently
and can be liquidated.

## Evidence

> **Evidence status** — source: `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3`,
> generated 2026-08-27. The only filed report is a triangular recording
> with **0 cycles executed** and no profitability statement. No carry,
> futures-against-futures or funding report has been filed. For every
> perpetuals strategy there is **no evidence yet**.

## Package limits that apply here

Venue count and refresh interval scale by package; perpetuals data is
available from Watch. Simulated carry positions require Operator; the
other two perpetuals strategies require Desk. [See the table](/pricing).

---

> **Risk summary.** {{brand}} measures and simulates; it does not trade,
> hold funds, hold your exchange keys or advise. Spreads, carry and
> simulated results are measurements net of modelled fees, not predictions.
> Many measured spreads cannot be traded: quotes move, depth is thin,
> transfers are slow or blocked, withdrawal status is unknown, venues fail.
> Simulations exclude transfers, assume top-of-book fills, model
> perpetuals at one-times notional with a hard stop, and use taker fees.
> Simulated results are prepared with hindsight and no account has traded
> them. Nothing on this page is a promise of any outcome. Read the full
> [Risk Disclosure](/legal/risk-disclosure).
