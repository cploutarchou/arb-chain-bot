---
title: About
slug: /about
status: DRAFT — compliance-reviewer sign-off required before publish
drafted: 2026-08-27
owner: content-copywriter
constraints: review-2026-08-27 items 3 (company identity), 4, 24; no persona, no invented team stories
---

## H1: A measurement tool that files its own reports.

{{brand}} began as an internal triangular-arbitrage engine with one job:
find out, with fees included and without wishful rounding, whether the
cycles it saw were worth anything. The answer so far has been recorded in
reports rather than in claims. The product you can use today is that
engine, widened to cross-venue spot and perpetuals, with alerting and
automatic paper execution built around it.

## What we believe

**A number without a source is marketing.** Every figure we publish is
quoted from a report in our campaign archive, with its fees and its
verdict. When there is no report, the page says "no evidence yet".

**Fees are the strategy.** Most measured spreads are smaller than the cost
of crossing them. A screener that shows gross spreads is showing you the
wrong number.

**Paper first, and honestly.** Simulated execution is only useful if the
model is conservative and its exclusions are printed next to the result.
Ours are: no transfers, top-of-book fills, unknown withdrawal status,
one-times perpetuals with a hard stop, taker fees on every leg.

**Not a broker, not an adviser.** The product does not trade, does not
hold funds, does not take exchange keys and does not recommend. That is
not a phase. Any decision to offer execution would be a separate product,
a separate legal decision, and would require evidence that does not yet
exist.

## How it is run

- Money arithmetic is decimal on every path.
- Live execution is disabled in code, in every environment.
- Exchange credentials have no place in the client product; the operator's
  own credential store is write-only and unread.
- Collectors, rule evaluation, paper execution, reports, backups and
  deploys run unattended and alert on failure.
- Venues are added through one connector interface with a conformance
  test, and each is labelled with what is verified and what is not.

## Company

{{company}} · registered number [n] · registered office [address] ·
contact [e-mail] [COUNSEL: mandatory identity fields per market].

## Contact

Support: [support e-mail] · Security: [security e-mail] · Privacy:
[privacy e-mail] · Press: [press e-mail]

---

{{brand}} is a market-data, analytics and simulation tool. It does not
execute trades, hold funds, hold exchange keys or give advice.
[Risk Disclosure](/legal/risk-disclosure)
