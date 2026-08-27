---
title: Triangular engine
slug: /product/triangular
status: DRAFT — compliance-reviewer sign-off required before publish
drafted: 2026-08-27
owner: content-copywriter
evidence: docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3 (zero qualified cycles)
constraints: review-2026-08-27 items 4, 5, 6, 7; strategy-models.md §0, §6, §8
---

## Hero

**Eyebrow:** Product · Triangular

**H1:** Three legs, one venue, one honest number at the end.

**Sub:** The triangular engine evaluates cycles that start and end in the
same asset on a single venue — through two intermediate pairs — with
taker fees on every leg, step-size truncation, and the same paper
executor as the rest of the suite. It is the oldest part of {{brand}},
and the only part with a filed report.

**CTA:** Run it on the Operator trial

## What a cycle is

Start asset → pair one → pair two → pair three → start asset, all on one
venue, all at executable sides. The planner prices the whole loop from
current quotes, subtracts three taker fees, truncates each quantity to the
venue's step size, and reports the gross and net edge in basis points of
the input.

## What the engine checks before it counts a cycle

- **Net edge above your threshold** after fees and buffers.
- **Depth** on every leg for the requested size; partial fills are refused
  unless your rule allows them.
- **Data age** on all three quotes.
- **Fee verification status** of the venue; unverified fees are scanned
  but flagged.

## What paper execution records

- Each leg's planned price, simulated fill price, latency draw, and
  whether the limit-IOC re-read accepted or rejected it.
- Whether the cycle returned to the start asset ("all filled") or stopped
  part-way, with the leftover asset booked at its cost.
- Realised slippage against the plan, per cycle.
- Fees paid, in the asset they were charged in.
- Net result in the start asset and in basis points of input.

## Evidence

> **Evidence status** — source: `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3`,
> generated 2026-08-27T01:36:02Z. Paper simulation over recorded market
> data with seeded latency and replayed books; eight scenarios (baseline;
> fees plus 5 bps; fees plus 10 bps; latency ×2; latency ×4; fills at
> half; liquidity at half; adverse combination), three seeds each.
>
> Result in every scenario: **cycles 0, all-filled 0, fees paid 0, net
> PnL 0 USDT, turnover 0, max drawdown 0.0000.**
>
> Verdict, verbatim: "NO CYCLES EXECUTED in the baseline — the recording
> produced no qualified opportunities; no profitability statement can be
> made from it."
>
> Reading this honestly: on that recording, at the thresholds set, the
> venue's books never offered a cycle that cleared three taker fees. That
> is a finding about the market and the threshold, not a result about
> profit. **No evidence yet.** Longer recordings are scheduled; each will
> be filed and linked here with its verdict.

## Why we publish a zero

Because a product that only publishes wins is selling a story. A filed
report that says "nothing qualified" tells you what the engine actually
saw, and it is the baseline the next report is measured against.

## Package limits that apply here

Triangular venues: one on Watch, two on Signal, four on Operator, all
supported venues on Desk and Institution. Paper execution of triangular
cycles requires Operator or above. [See the table](/pricing).

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
