---
title: Risk Disclosure
slug: /legal/risk-disclosure
status: DRAFT for legal review — not published
version: 0.1-draft
drafted: 2026-08-27
owner: content-copywriter
reviewers: compliance-reviewer, outside counsel
constraints: docs/compliance/review-2026-08-27.md items 3, 5, 6, 7, 8, 21; docs/design/packages.md §7; docs/design/strategy-models.md §0, §8
acceptance: shown in full at sign-up; versioned; accepted with timestamp and IP; linked from every spread, carry, funding and PnL surface, every export, every API response and every alert footer
---

<!-- Every strategy page ends with the "Summary" section below, verbatim.
     Alerts and reports carry the "Simulated results" paragraph as a footer.
     No figure appears in this document. -->

# Risk Disclosure

**Version:** [n] · **Effective:** [date]

Read this before you use {{brand}}. You will be asked to confirm that you
have read it when you create an account, and again when this text changes.

## 1. What {{brand}} is, and is not

{{brand}} measures price differences between public quotes on crypto
venues, applies fee models, and records what a simulated order would have
done. It sends you alerts for conditions **you** configured.

It does **not** trade, hold funds, hold your exchange keys, or advise. No
package offers live execution. Nothing in the product, on the website, in
an alert or in a report is a recommendation to buy, sell, hold or move any
asset, and nothing takes your personal circumstances into account.

## 2. A measured spread is not a tradeable profit

The differences the screener shows are **measurements** of quotes at a
point in time, net of the fees in our model. They are not predictions and
they are not profit. In practice:

- **Quotes move.** By the time an order could reach a venue, the prices that
  made the spread may be gone. Two legs rarely fill at the prices displayed.
- **Depth is thin.** The displayed price may exist only for a small
  quantity. Larger orders walk the book and pay more.
- **Transfers are slow or blocked.** Moving assets between venues takes
  time, costs fees, and may be suspended without notice. Our default model
  assumes inventory already sits on both venues and **excludes transfers**.
- **Withdrawal status is unknown.** We cannot verify whether a venue
  currently allows deposits or withdrawals of a given asset on a given
  network. A spread on an asset you cannot move may be untradeable.
- **Fees differ from the model.** Venue fees change, tiers differ, some
  fee schedules are unverified, and token discounts are not modelled.
  Our model uses taker fees on every leg and says which venue fees are
  verified and which are not.
- **Venues fail.** Outages, halted trading, delisted pairs, frozen accounts
  and insolvency have all happened on crypto venues and can happen again.
  Counterparty risk is yours.

## 3. Derivatives, basis and funding

Perpetual futures are leveraged derivatives. Positions can be liquidated;
funding rates change sign; predicted funding is not settled funding; a
basis can widen before it narrows. Our model treats perpetual legs at
one-times notional with a hard stop, which is a **simulation choice**, not
a description of how a real account behaves.

Derivatives are unavailable or restricted for retail clients in some
countries [COUNSEL: item 8 — UK retail ban, US access to offshore
perpetuals, EU MiFID scope]. Where we show perpetuals, basis or funding
data, it is **information only**; availability to you depends on your
country and your venue's rules, not on us.

## 4. Simulated results

Simulated ("paper") results have inherent limitations. Unlike a real
account they do not involve financial risk, and no simulated record can
fully account for the impact of real trading: the inability to withstand
losses, the effect of one's own orders on the market, or the decision to
follow a rule when it is losing. Simulated results are generally prepared
with the benefit of hindsight. There are frequently sharp differences
between simulated results and results achieved later by any real trading.
No representation is made that any account will or is likely to achieve
profits or losses similar to those shown.

Our specific model exclusions: **no transfers between venues; fills at the
top of the book with a fixed slippage allowance; withdrawal availability
unknown; perpetual legs at one-times notional with a hard stop; taker fees
on every leg; token discounts off.** These exclusions are printed on every
report, export and alert.

Simulated balances are **not** money. Where a ledger shows a balance, it is
labelled "simulated balance" and results are shown first in basis points or
percent of the amount deployed, so that the size of a starting balance does
not make a result look like a return.

## 5. Evidence status

We publish results only from reports in our campaign archive, quoted
verbatim with fees and verdicts. Where no report supports a claim, we say
"no evidence yet". As of the effective date of this version, no published
report shows a positive simulated result for any strategy.

## 6. Your responsibility

Any trading decision is yours alone, made on a venue you chose, with funds
you control, under that venue's terms. Crypto assets are volatile and can
lose all their value. Do not trade with money you cannot afford to lose.
Consider independent advice. Nothing here is promised, and no part of
this product is free of risk or a source of income.

[COUNSEL: item 19 — if the UK cryptoasset financial-promotion regime
applies, insert the prescribed risk warning text and 24-hour cooling-off
mechanics; item 17 — VARA marketing rules; item 20 — CFTC-style language
if US users are admitted.]

---

## Summary (appended to every strategy page)

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
