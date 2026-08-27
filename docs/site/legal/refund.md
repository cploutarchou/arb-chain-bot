---
title: Refund and Cancellation Policy
slug: /legal/refund
status: DRAFT for legal review — not published
version: 0.1-draft
drafted: 2026-08-27
owner: content-copywriter
reviewers: compliance-reviewer, billing-engineer, outside counsel
constraints: docs/compliance/review-2026-08-27.md item 11; docs/design/packages.md §4
---

<!-- Timings below mirror docs/design/packages.md §4, which is a PROPOSAL.
     Any change there must be reflected here before publication.
     No currency or percentage figure appears in this file. -->

# Refund and Cancellation Policy

**Version:** [n] · **Effective:** [date]

Paddle is the merchant of record for every payment. Where this policy and
Paddle's buyer terms differ, [COUNSEL: state which prevails, item 11].

## 1. Trial

- New organisations receive a 14-day trial of the Operator package, without
  a payment method, once per organisation.
- Trial days are not credited against a later purchase.
- If no paid subscription starts by the end of day 14, the organisation
  moves to the free Watch package on day 15:
  - alert rules above the Watch limit are paused (not deleted);
  - open simulated positions are closed on paper;
  - data beyond the Watch history depth is kept for 30 days, then trimmed
    to the Watch depth;
  - a data export is offered before trimming.

## 2. Money-back period

- On the **first paid period of a new subscription**, you may request a full
  refund within 14 days of payment, once per organisation.
- After a refund the organisation returns to Watch immediately.
- This applies whether you pay monthly or annually.

## 3. Statutory withdrawal right (consumers)

If you are a consumer in the EU, UK or another jurisdiction with a
statutory right of withdrawal, you have the right to cancel within 14 days
of purchase. Because the Service starts delivering immediately, at checkout
you will be asked to **expressly consent to immediate performance and
acknowledge that you lose the right of withdrawal once performance has
begun** [COUNSEL: exact wording; whether our money-back period should
simply satisfy the statutory right instead; treatment of business buyers
who declared consumer status incorrectly]. Our money-back period in §2
applies in any case.

## 4. Upgrades

- Upgrades take effect when our payment processor confirms the change; the
  console shows "pending" until then.
- Within a billing period, an upgrade is charged pro rata for the remainder
  of the period. Monthly to annual starts a new annual period on the day of
  the change.
- If you downgrade again within 72 hours of an upgrade, the unused portion of
  the upgrade charge is returned as account credit.

## 5. Downgrades

- Downgrades (lower package, or annual to monthly) are scheduled for the end
  of the current period. No refund or credit is given for the remainder.
- Your current limits remain until then. Afterwards, data and rules above
  the new limits are gated, kept read-only for 30 days, then removed.

## 6. Cancellation

- Cancel at any time from the console. The subscription ends at period end;
  there is no refund for the remainder except under §2 or §3.
- Immediate cancellation is available only through support.
- On cancellation the organisation moves to Watch at period end; data is
  kept for 30 days for export, then trimmed.

## 7. Failed payments

If a renewal payment fails, our processor retries for 21 days. Full access
continues for the first 7 days; after that the organisation becomes
read-only (alerts and simulated execution off, API read-only) until
payment succeeds. If the retries fail, the organisation moves to Watch.

## 8. Seats and quotes

Extra seats on Desk and Institution are charged pro rata for the current
period and are not refundable when removed mid-period. Institution
contracts are invoiced annually and follow the terms in the contract.

## 9. How to request a refund

Write to [billing e-mail] from the owner's account address with the
organisation name. Refunds are issued to the original payment method by our
processor; timing depends on your bank.

[COUNSEL: item 20 — US state auto-renewal and cancellation-mechanism laws;
item 18 — EU unfair-terms review of §5 no-refund-on-downgrade.]
