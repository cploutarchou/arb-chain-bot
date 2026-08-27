---
title: Privacy Policy
slug: /legal/privacy
status: DRAFT for legal review — not published
version: 0.1-draft
drafted: 2026-08-27
owner: content-copywriter
reviewers: compliance-reviewer, outside counsel (data protection)
constraints: docs/compliance/review-2026-08-27.md items 3, 9, 10, 20, 22
---

<!-- {{company}} / {{brand}} tokens; [COUNSEL] marks jurisdiction-dependent text.
     The Art. 30 data map (item 9) is a prerequisite: every row in §3 must
     match it before publication. -->

# Privacy Policy

**Last updated:** [date] · **Version:** [n]

This policy explains what personal data {{company}} ("we") collects when you
use {{brand}}, why, for how long, and what rights you have.

## 1. Who is responsible

{{company}} is the controller for the data described here [COUNSEL: confirm
controller/processor split; for organisation-member data uploaded by an owner
we act as processor under the DPA]. Contact: [privacy e-mail]. Data
protection officer / representative: [name or "not required"] [COUNSEL: EU
Art. 27 representative if established outside the EU; UK representative].

## 2. What we do not collect

- **Exchange API keys.** The Service does not ask for, accept or store your
  exchange credentials. Any operator-side credential storage is internal and
  never linked to a client account.
- **Wallet addresses or fund balances.** We do not connect to your accounts.
- **Payment card data.** Our payment processor collects it; we receive only
  a customer reference, subscription state and invoice metadata.

## 3. What we collect and why

| Data | Source | Purpose | Lawful basis [COUNSEL] | Retention |
|---|---|---|---|---|
| Account: e-mail, name, password hash, country, business/consumer status | you | account, contract, statutory-rights routing, content gating | contract; legal obligation | life of account + [n] months |
| Organisation: name, members, roles | you | multi-user access | contract | life of organisation + grace |
| Acceptance record: terms/risk-disclosure version, timestamp, IP | your device | proof of acceptance | legitimate interest; legal obligation | [n] years after account closure |
| Configuration: rules, templates, thresholds, venue selections | you | run the Service | contract | package history depth; 30-day grace after downgrade/cancel |
| Alert destinations: Telegram chat id, e-mail, webhook URL | you | deliver alerts | contract | until you remove the destination or the rule/organisation is deleted; encrypted at rest; never written to logs |
| Simulated ledgers and reports | generated | your evidence | contract | package history depth |
| Usage and device data: pages, actions, browser, approximate location from IP | your device | security, capacity, product analytics (consent-gated) | legitimate interest (security); consent (analytics) | [n] months |
| Audit log: who changed what, when | generated | security, dispute resolution | legitimate interest; legal obligation | [n] years; identities pseudonymised on erasure |
| Support correspondence | you | support | contract | [n] years |
| Billing metadata from Paddle: customer id, plan, status, invoices | Paddle | billing, tax records | contract; legal obligation | statutory accounting period |
| Affiliate data: name, country, payout details, tax forms | you | affiliate payouts, tax reporting | contract; legal obligation | statutory period |

We do not perform automated decision-making with legal or similarly
significant effects on you.

## 4. Who we share data with

- **Subprocessors** listed at [/legal/subprocessors](/legal/subprocessors)
  (hosting, e-mail delivery, Telegram for alerts, payment processing,
  error monitoring, analytics under consent).
- **Legal.** Authorities where required by law; advisers under
  confidentiality.
- **Successor.** A buyer of the business, under the same commitments.

We do not sell personal data and do not share it for third-party
advertising.

## 5. International transfers

Some subprocessors operate outside your country. Where the law requires,
transfers rest on [COUNSEL: EU SCCs / UK IDTA / adequacy decisions; note
Telegram as a third-country processor for chat ids, item 10].

## 6. Security

Encryption in transit and at rest; role-based access; audit logging;
alert destinations encrypted with a separate key; production access
restricted to named operators; backups tested. No system is perfectly
secure; we will notify you and the regulator of a breach as the law
requires [COUNSEL: 72-hour rule and equivalents].

## 7. Your rights

Depending on where you live you may have the right to access, correct,
delete, restrict, port or object to processing of your data, and to
withdraw consent. To exercise them, use the account settings or write to
[privacy e-mail]. We respond within the statutory period.

- **Portability.** A free export of your personal data is available from
  account settings regardless of package. This is separate from the paid
  market-data export feature.
- **Erasure.** We delete or pseudonymise your data, except where we must
  keep it (billing records, acceptance records, fraud prevention).
- **Complaints.** You may complain to your supervisory authority [COUNSEL:
  name the lead authority].

[COUNSEL: add CCPA/CPRA notice at collection, "do not sell or share"
statement, and categories table for California residents, item 20.]

## 8. Cookies and analytics

See the [Cookie Policy](/legal/cookies). Analytics run only after consent.

## 9. Children

The Service is not directed at people under 18 and we do not knowingly
collect their data.

## 10. Changes

We will announce material changes by e-mail and in the console before they
take effect.
