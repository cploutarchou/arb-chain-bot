---
title: FAQ
slug: /faq
status: DRAFT — compliance-reviewer sign-off required before publish
drafted: 2026-08-27
owner: content-copywriter
evidence: docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3 (zero qualified cycles)
constraints: review-2026-08-27 items 4, 5, 6, 7, 8, 11, 13, 21, 22, 24
---

## What the product is

**Does {{brand}} trade for me?**
No. It measures quotes, applies fee models, sends alerts you configured,
and simulates execution on paper. No order is ever sent to a venue.

**Will it trade for me later, on a higher package?**
No package offers live execution, and none will as an upgrade. Live
execution is disabled in the software. Offering it would be a separate
product and a separate legal decision, and it would require evidence that
does not exist today.

**Do I give you my exchange API keys?**
No. There is nowhere in the product to enter them, and we do not want
them.

**Is this financial advice?**
No. Alerts and screens report measurements against thresholds you set.
Nothing takes your circumstances into account, and nothing is a
recommendation to buy, sell, hold or transfer anything.

## Spreads and results

**The screener shows a spread. Can I trade it?**
Often not. The number is a measurement of quotes at one moment, net of
modelled fees. Quotes move, depth may be thin, transfers between venues
are slow or blocked, and we cannot see whether a venue currently allows
withdrawals. The [Risk Disclosure](/legal/risk-disclosure) lists the
reasons.

**What does "net" mean on a row?**
Sell-side bid after the sell fee, against buy-side ask after the buy fee,
with taker fees at each venue's placement rule. Mid prices are never used.

**Which venue fees are verified?**
Each venue is labelled in the console. Where a fee schedule has not been
read from the venue's published page on a stated date, the label says
"unverified" and the row is flagged.

**How well has it performed?**

> **Evidence status** — source: `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3`,
> generated 2026-08-27. One triangular recording; eight stress scenarios;
> **0 cycles executed**. Verdict verbatim: "no profitability statement can
> be made from it." **No evidence yet** for any strategy. When a report
> with executed samples exists, it will be linked with its fees and its
> verdict, positive or negative.

**Why would I pay for a tool with no positive evidence?**
Because the evidence is the product. You configure the rules; the suite
executes them on paper, with your thresholds and your venues, and files
the result. Whether a given idea survives fees is precisely what you are
paying to find out.

**Are simulated results realistic?**
They are conservative in stated ways and optimistic in others. Conservative:
taker fees on every leg, top-of-book fills with a slippage allowance,
rejected legs when the price moves past a tolerance. Optimistic: no
transfers, no withdrawal checks, no market impact from your own orders,
and hindsight. Every report prints these exclusions.

**Why is my paper balance shown that way?**
Balances are labelled "simulated balance" and results are shown first in
basis points or percent of the amount deployed, so that a large starting
balance cannot make a small result look like a large return.

## Perpetuals

**Can I use the perpetuals monitor in my country?**
The monitor is information. Whether you can trade a perpetual depends on
your country and your venue. In some countries retail clients cannot
trade crypto derivatives at all; the console shows a notice based on the
country you declared at sign-up. [COUNSEL: item 8 — confirm gating
approach.]

**Is the funding rate shown annualised?**
In the console, per-interval rates are shown as published; an annualised
figure may be shown for display only and is labelled as such. Marketing
pages do not show funding figures.

## Alerts

**What is in an alert?**
The rule name, the venues, the pair, the measured figures, the data age,
and a fixed footer stating that the figures are measurements and, where
paper execution is attached, that the result is simulated. Alerts contain
no instruction to do anything.

**Why did an alert not arrive?**
Common reasons, all visible in the web inbox: daily quota reached (the
event is stored and marked "skipped: quota"); the channel is not included
in your package (delivered to the web inbox with a banner); cool-down
still running; data-age gate failed.

## Billing and account

**How does the trial work?**
Fourteen days of Operator, no card, once per organisation. At the end you
move to Watch: rules above 2 pause, simulated positions close on paper,
data above 24 hours is kept 30 days for export.

**Can I get a refund?**
First paid period of a new subscription, within 14 days, once per
organisation. Consumers in the EU and UK have statutory rights as well.
See the [Refund Policy](/legal/refund).

**Does a downgrade delete my data?**
Not immediately. Data above the new limits becomes read-only for 30
days, then is removed. Export is offered before that.

**Can I export my personal data?**
Yes, free, from account settings, on any package. This is separate from
the market-data export feature.

**Who is the seller?**
Paddle is the merchant of record. {{company}} operates the product.

## Comparisons

**How do you compare with other scanners?**
We do not publish comparisons. Package scope is described on the
[pricing page](/pricing); judge it against your own needs.

---

{{brand}} is a market-data, analytics and simulation tool. It does not
execute trades, hold funds, hold exchange keys or give advice.
[Risk Disclosure](/legal/risk-disclosure)
