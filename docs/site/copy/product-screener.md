---
title: Cross-venue screener
slug: /product/screener
status: DRAFT — compliance-reviewer sign-off required before publish
drafted: 2026-08-27
owner: content-copywriter
evidence: docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3 (zero qualified cycles)
constraints: review-2026-08-27 items 4, 5, 6, 7, 13; strategy-models.md §0, §1
---

## Hero

**Eyebrow:** Product · Screener

**H1:** Every pair, every venue, fees already subtracted.

**Sub:** The screener compares the ask on one venue with the bid on
another for every common pair, applies each venue's taker fee, checks that
both quotes are fresh, and shows you the net figure. You decide what
counts; it tells you when it happens.

**CTA:** Try it on the Operator trial

## How a row is computed

1. **Executable sides only.** The spread is always the sell-side bid net
   of the sell fee against the buy-side ask plus the buy fee. Mid prices
   never appear in a spread.
2. **Fees in.** Taker fee on both legs, using the venue's own placement
   rule. Each venue carries a label: **verified** (read from the venue's
   published schedule on a stated date) or **unverified**.
3. **Data-age gate.** Both quotes must be younger than your refresh
   interval, and their ages must be close to each other. A fresh leg
   against a stale leg manufactures a spread that never existed, so the
   row is dropped with the reason shown.
4. **Depth shown.** Top-of-book quantity on each side, so a spread that
   exists for a tiny size is visible as such.
5. **Decimal throughout.** Nothing is rounded before display.

## What you can do with it

- **Filter** by venue pair, quote asset, minimum net spread, minimum depth,
  maximum data age.
- **Sort** by net spread, gross spread, depth, age.
- **Save templates** and switch between them.
- **Turn a view into a rule** with a threshold and a cool-down; route it to
  web, Telegram, e-mail or webhook depending on your package.
- **Attach paper execution** so the rule does not just alert, it records
  what a simulated fill would have cost and returned (Signal and above).
- **Export** the history your package retains.

## What the screener will not do

- It does not know whether either venue currently allows deposits or
  withdrawals of the asset. A spread on an asset you cannot move is a
  number, not a trade.
- It does not model transfers between venues. The default model assumes
  inventory already sits on both sides.
- It does not pick assets for you. There are no default rules; every
  threshold is yours.
- It does not tell you to do anything. Alerts contain measurements and the
  name of your rule.

## Evidence

> **Evidence status** — source: `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3`,
> generated 2026-08-27. The only filed report to date is a triangular
> recording and it executed **0 cycles**; verdict verbatim: "no
> profitability statement can be made from it." For the cross-venue
> screener specifically there is **no evidence yet**. The Scanner Suite
> soak report has not been filed. This page will link it when it exists.

## Package limits that apply here

Venue count, refresh interval, active rules, saved templates, alert
channels and history depth all scale by package. [See the table](/pricing).

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
