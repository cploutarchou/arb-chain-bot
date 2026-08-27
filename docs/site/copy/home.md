---
title: Home
slug: /
status: DRAFT — compliance-reviewer sign-off required before publish
drafted: 2026-08-27
owner: content-copywriter
evidence: docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3 (zero qualified cycles)
constraints: review-2026-08-27 items 4, 5, 6, 7, 13, 24; packages.md §2 "Returns and evidence", §7
---

<!-- Tokens: {{brand}}. No percentage or currency figure outside the
     "Evidence" block. No competitor names. Watch-tier live numbers are
     labelled "live screener sample", never "results". -->

## Hero

**Eyebrow:** Market data · alerts · simulated execution

**H1:** Measure the spread. Then find out whether it was real.

**Sub:** {{brand}} watches quotes across crypto venues, nets out the fees,
alerts you when a condition you set is met, and then does something the
alert alone cannot: it executes every signal on paper and files the
result. Not a promise. A record.

**Primary CTA:** Start a 14-day Operator trial — no card
**Secondary CTA:** Read the evidence so far

**Under-CTA line:** {{brand}} does not trade, hold funds or take exchange
keys. Simulated results are labelled simulated everywhere.

## Live screener sample (Watch tier feed)

**Label above table:** Live screener sample · refreshes every 30 s · three
fixed venues · net of modelled taker fees · measurement, not a result

**Empty-state:** No pair currently meets the sample threshold. That is a
normal reading, and it is the kind of reading the product is built to
show you honestly.

**Under-table:** These are measurements of public quotes at the moment
shown. They are not tradeable prices, not predictions, and not campaign
results. [Why a measured spread is not a profit](/legal/risk-disclosure).

## What it does

### Cross-venue screener
Every listed pair on every enabled venue, ask against bid, fees in, data
age checked on both legs. Filter, sort, save the view, turn it into a
rule.

### Perpetuals monitor
Basis and funding across venues, predicted and settled rates kept apart,
next settlement visible. Information only where your country requires.

### Triangular engine
Intra-venue cycles evaluated with the same fee model and the same
paper executor. The oldest part of the product, and the first with a
filed report.

### Automatic paper execution
Every rule can execute on paper the moment it fires, with modelled fees,
top-of-book fills and a seeded latency model. Each strategy gets its own
simulated ledger and a nightly report.

## Why paper first

Most alert products stop at the alert. We think the interesting question
is the next one: **would it have worked?** So each signal is executed in a
simulation you can inspect, scored net of fees, and written up. When the
evidence is bad, the report says so. When there is no evidence, we say
that too.

## Evidence

> **Evidence status** — source: `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3`,
> generated 2026-08-27T01:36:02Z. Paper simulation over recorded market
> data, eight stress scenarios, three seeds each. **Cycles executed: 0.**
> Verdict, verbatim: "NO CYCLES EXECUTED in the baseline — the recording
> produced no qualified opportunities; no profitability statement can be
> made from it."
>
> That is the whole published record today. **No evidence yet** of a
> positive simulated result for any strategy. We will link every future
> report here, with its fees and its verdict, whether it is good or bad.

## How it is built

- **Decimal money math** on every path; nothing is rounded until display.
- **Taker fees on every leg**, verification status shown per venue.
- **Data-age gate** on both legs, so a fresh quote is never compared with
  a stale one.
- **Live execution disabled in the code**, in every environment, for every
  package. Not a setting, not an upgrade.
- **Exchange keys are never requested.** There is nowhere in the client
  product to enter one.

## Packages

Watch (free, three fixed venues, web alerts, 24-hour history) · Signal ·
Operator · Desk · Institution. Higher packages buy more venues, rules,
simulated positions, history and support — never a different product.
[Compare packages](/pricing)

## Closing CTA

**H2:** Start measuring.
**Body:** Fourteen days of Operator, no card, one trial per organisation.
When it ends you keep a Watch account and your exported data.
**CTA:** Create an organisation

## Footer risk line (site-wide)

{{brand}} is a market-data, analytics and simulation tool. It does not
execute trades, hold funds, hold exchange keys or give advice. Spreads and
simulated results are measurements net of modelled fees, not predictions.
Crypto assets can lose all their value. [Risk Disclosure](/legal/risk-disclosure)
