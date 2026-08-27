---
title: Subprocessors
slug: /legal/subprocessors
status: DRAFT for legal review — not published
version: 0.1-draft
drafted: 2026-08-27
owner: content-copywriter
reviewers: compliance-reviewer, infra-sre, outside counsel
constraints: docs/compliance/review-2026-08-27.md items 9, 10; docs/design/crypto-arb-platform-command.md §10
---

<!-- Vendor names below are PLACEHOLDERS except Paddle and Telegram, which
     are named in the design documents. infra-sre confirms the final list
     before publication. Market-data venues are NOT subprocessors: we read
     their public interfaces and send them no personal data. -->

# Subprocessors

**Version:** [n] · **Effective:** [date]

{{company}} uses the third parties below to deliver {{brand}}. Each one
processes personal data only for the purpose stated and under a written
agreement. We announce additions at least 30 days in advance to
organisation owners (see the [DPA](/legal/dpa) §4).

| Subprocessor | Purpose | Data | Location of processing | Transfer mechanism [COUNSEL] |
|---|---|---|---|---|
| Paddle | merchant of record: checkout, subscriptions, invoices, tax, affiliate payouts | name, e-mail, billing address, payment method (held by Paddle), subscription state | [Paddle region] | [SCCs / adequacy] |
| Telegram | delivery of alerts to chats the customer configured | chat id, alert text (measurements, rule name, simulated footer) | third country | [COUNSEL: mechanism and transfer-impact assessment; item 10] |
| [Cloud hosting provider] | compute, managed database, object storage, backups | all service data, encrypted at rest | [region] | [mechanism] |
| [Transactional e-mail provider] | account e-mails, alert e-mails, billing notices | e-mail address, message content | [region] | [mechanism] |
| [Error and performance monitoring] | crash and latency diagnostics | pseudonymous ids, request metadata; no alert content | [region] | [mechanism] |
| [Product analytics] | consent-gated usage analytics | pseudonymous ids, page and event names | [region] | [mechanism] |
| [Support helpdesk] | support tickets | name, e-mail, ticket content | [region] | [mechanism] |
| [Log and metrics platform] | operations logging (alert destinations are never logged) | request metadata, operator identities | [region] | [mechanism] |

## Not subprocessors

- **Crypto venues** (exchanges and DEX aggregators): we read their public
  market-data interfaces. No personal data is sent to them, and we hold no
  credentials for your accounts there.
- **Webhook endpoints you configure**: you are the controller of what you
  send to your own systems.

## Change log

| Date | Change |
|---|---|
| [date] | initial list |
