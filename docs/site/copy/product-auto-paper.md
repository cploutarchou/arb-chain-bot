---
title: Automatic paper execution
slug: /product/auto-paper
status: DRAFT — compliance-reviewer sign-off required before publish
drafted: 2026-08-27
owner: content-copywriter
evidence: docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3 (zero qualified cycles)
constraints: review-2026-08-27 items 4, 5, 6, 21; packages.md §7; strategy-models.md §0, §1, §8
---

## Hero

**Eyebrow:** Product · Auto-paper

**H1:** Every signal, executed on paper, scored net of fees, written up.

**Sub:** An alert says a condition was met. Paper execution answers the
harder question: at that moment, with these fees, this depth and this
latency, what would a simulated order have done? {{brand}} runs that
simulation for every rule you enable, keeps a separate ledger per
strategy, and files a report on a schedule your package sets.

**CTA:** Enable it on the Operator trial

## What "paper" means here

- **No order is sent anywhere.** The executor has no venue credentials and
  live execution is disabled in the code for every package.
- **Fills are simulated** at the top of the book with a fixed slippage
  allowance per rule, after a seeded latency draw. If the re-read price is
  worse than a tolerance, the leg is rejected, not filled.
- **Fees are taker fees on every leg**, at the venue's placement rule.
- **Sizing is capped** per execution by your package, in decimal, and
  never leveraged: a perpetual leg posts its full notional as simulated
  collateral.
- **One open position per rule and base** at a time; reservations are
  taken against a simulated per-venue balance.

## The five strategies

| Strategy | What the ledger records | Available from |
|---|---|---|
| Cross-venue spot (inventory on both venues, no transfers) | matched pairs, per-leg fills, net after rebalance | Signal |
| Spot + perpetual carry (same venue) | entry basis, funding settlements booked at settled rate, unwind cost | Operator |
| Triangular | per-leg fills, all-filled status, realised slippage | Operator |
| Perpetual against perpetual (two venues) | basis at entry and exit, both funding streams | Desk |
| Funding-rate position | funding settlements, hedge cost, hard-stop events | Desk |

## Ledgers and balances

Each strategy has its own simulated ledger. Balances are labelled
**simulated balance** wherever they appear. Results are shown **first in
basis points or percent of the amount deployed**, then in quote
currency, so that the size of a starting balance cannot make a result
look like a return.

## Reports

Nightly (Operator and above; weekly on Signal), one per strategy, per
rule set, per regime (calm, volatile, weekend). Each report lists sample
size, gross and net per sample, realised slippage against the allowance,
fees paid, failed-cycle costs, drawdown and concentration, and ends with
a verdict. Reports are the only source from which we quote any number in
public.

## The production gate, stated plainly

Live execution stays disabled until, for a strategy and rule set, at
least 30 consecutive days of unattended paper execution show a positive
net result in every regime, pass a statistical test at a stated sample
size, survive a fee-and-fill stress grid, stay within drawdown and
concentration limits, and show realised slippage inside the allowance —
and then only after a security review, a recorded legal decision, and a
human-reviewed code change. Passing the gate is a necessary condition for
a separate future product decision. It is not on any package's roadmap
and is not an upgrade path.

## Evidence

> **Evidence status** — source: `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3`,
> generated 2026-08-27. One triangular recording, eight stress scenarios,
> **0 cycles executed**, no profitability statement. No report yet exists
> for the other four strategies. For automatic paper execution as a whole:
> **no evidence yet.**

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
