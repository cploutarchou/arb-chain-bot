---
title: Data Processing Addendum
slug: /legal/dpa
status: DRAFT for legal review — not published
version: 0.1-draft
drafted: 2026-08-27
owner: content-copywriter
reviewers: compliance-reviewer, outside counsel (data protection)
constraints: docs/compliance/review-2026-08-27.md items 3, 9, 10, 22
---

<!-- Applies when a customer organisation (the "Customer") uploads or
     causes us to process personal data of its members or contacts.
     Heavily [COUNSEL]: this is a structural draft, not final wording. -->

# Data Processing Addendum

**Version:** [n] · **Effective:** [date]

This Addendum forms part of the [Terms of Service](/legal/terms) between
{{company}} ("Processor") and the Customer ("Controller") and applies to
the extent {{company}} processes personal data on the Customer's behalf.

## 1. Roles

- For **Customer-supplied data** (member names and e-mails, alert
  destinations, rule names, notes), the Customer is controller and
  {{company}} is processor.
- For **account, billing, security and usage data** described in the
  [Privacy Policy](/legal/privacy), {{company}} is an independent
  controller.
[COUNSEL: confirm split; joint-controller analysis for audit log identities.]

## 2. Subject matter and details of processing

| Item | Description |
|---|---|
| Subject matter | provision of the {{brand}} service |
| Duration | term of the contract plus the retention grace in the Refund Policy |
| Nature and purpose | storing configuration, delivering alerts, generating simulated ledgers and reports, providing console and API access |
| Categories of data subjects | Customer's members, invited users, recipients of forwarded alerts |
| Categories of personal data | name, e-mail, role, Telegram chat id, webhook URL, IP address, activity records |
| Special categories | none intended; Customer must not upload any |

## 3. Processor obligations

{{company}} will:
1. process personal data only on the Customer's documented instructions
   (the Terms and console settings are the instructions) unless required by
   law, in which case we inform the Customer where permitted;
2. ensure staff with access are bound by confidentiality;
3. implement the technical and organisational measures in Annex 2;
4. engage subprocessors only under §4;
5. assist the Customer with data-subject requests, security, breach
   notification and impact assessments, taking into account the nature of
   the processing;
6. delete or return personal data at the end of the contract, subject to
   legal retention, with export available from the console;
7. make available the information necessary to demonstrate compliance and
   allow audits under §6;
8. notify the Customer of a personal-data breach affecting Customer data
   without undue delay and no later than [n] hours after becoming aware
   [COUNSEL: align with the 72-hour runbook, item 15].

## 4. Subprocessors

- The current list is published at [/legal/subprocessors](/legal/subprocessors).
- We give at least 30 days' notice of additions by e-mail to the
  organisation owner. The Customer may object on reasonable data-protection
  grounds; if we cannot resolve the objection the Customer may terminate the
  affected service with a pro-rata refund of prepaid fees [COUNSEL].
- We flow down obligations no less protective than this Addendum.

## 5. International transfers

Transfers outside the EEA/UK rest on [COUNSEL: EU SCCs (module 2/3), UK
IDTA or Addendum, adequacy decisions]. Annex 3 lists the mechanism per
subprocessor. Telegram is a third-country processor for chat ids used to
deliver alerts the Customer configured; chat ids are encrypted at rest, not
logged, and deleted with the rule or organisation.

## 6. Audit

Once per year, or after a breach, the Customer may request written evidence
of compliance (policies, third-party reports where available). On-site
audits only where required by law or a supervisory authority, with
reasonable notice and confidentiality.

## 7. Liability

Subject to the Terms' limitation of liability [COUNSEL: whether a separate
DPA cap is required].

## Annex 1 — Instructions

Processing as required to provide the Service in accordance with the
Terms, the console settings the Customer chooses, and the Customer's
documented written instructions.

## Annex 2 — Technical and organisational measures

- Encryption in transit (TLS) and at rest; separate key for alert
  destinations and any secret material; key rotation.
- Role-based access; least privilege; named operator access to production;
  audit logging of privileged actions.
- Tenant isolation enforced server-side on every request.
- Backups with tested restores; secret material excluded from routine
  backups.
- Vulnerability management, dependency scanning, staged deploys.
- Incident response runbook with a breach-notification path.
- Retention jobs that enforce package history depth and grace periods;
  erasure by deletion or pseudonymisation of audit identities.

## Annex 3 — Subprocessors and transfer mechanisms

See [/legal/subprocessors](/legal/subprocessors); maintained as the
authoritative list.
