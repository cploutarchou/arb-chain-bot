---
title: Pricing
slug: /pricing
status: DRAFT — compliance-reviewer sign-off required before publish; prices pending operator sign-off
drafted: 2026-08-27
owner: content-copywriter
source: docs/design/packages.md §2 (PROPOSAL), §4, §7
constraints: review-2026-08-27 items 4, 11, 21, 24, 25; packages.md "Returns and evidence"
---

<!-- Prices are NEVER hard-coded in copy. The page renders Paddle price
     previews via {{price.<code>.<period>}} tokens so tax and currency are
     correct (packages.md §4). Limits below are counts from packages.md §2
     and are proposals until the operator confirms them. No % or currency
     figure appears in this file. No competitor is named. -->

## Hero

**H1:** Five packages. One product. No package trades.

**Sub:** Every package is the same measurement-and-simulation tool. Higher
packages buy more venues, faster refresh, more rules, more simulated
positions, deeper history and closer support. None buys execution, and
none buys a different regulatory posture.

**Toggle:** Monthly · Annual (annual billing includes two months at no
charge)

## Package cards

### Watch — free
{{price.watch.monthly}}
The live demo. Real quotes at a delayed refresh, so you can see what the
screener does instead of reading about it.
- 3 fixed venues; 1 triangular venue
- 2 active rules; 3 saved templates
- 30-second refresh
- Web inbox alerts, 20 per day
- Manual paper only; 1 simulated ledger
- 24 hours of history; public sample reports
- 1 seat; community docs
**CTA:** Create a Watch account

### Signal
{{price.signal.monthly}} / {{price.signal.annual}}
For one person who wants alerts off the console and the first strategy on
paper.
- 6 Tier-1 venues; 2 triangular venues
- 8 active rules; 10 templates
- 10-second refresh
- Web + Telegram, 200 alerts per day
- Auto-paper: cross-venue spot; 5 open simulated positions; 1 ledger
- 14 days of history; CSV export; weekly reports on your own rules
- 1 seat; e-mail support, 2 business days
**CTA:** Choose Signal

### Operator — trial package
{{price.operator.monthly}} / {{price.operator.annual}}
The package the 14-day trial gives you. Three strategies on paper, nightly
reports, a read API.
- All Tier-1 and Tier-2 venues; 4 triangular venues
- 25 active rules; 40 templates
- 5-second refresh
- Web + Telegram + e-mail, 1,500 alerts per day
- Auto-paper: cross-venue spot, carry, triangular; 30 open simulated
  positions; 3 ledgers
- Read API, 2 keys
- 90 days of history; CSV export; nightly reports
- 3 seats: owner, admin, viewer; e-mail support, 1 business day
**CTA:** Start the 14-day trial — no card

### Desk
{{price.desk.monthly}} / {{price.desk.annual}}
For a team that wants all five strategies on paper, webhook routing, and
enough history to do its own evaluation of our evidence.
- All CEX venues; all supported triangular venues
- 80 active rules; unlimited templates
- 3-second refresh
- Web + Telegram + e-mail + webhook, 8,000 alerts per day
- Auto-paper: all five strategies; 150 open simulated positions; 10 ledgers
- Read + write API for rules and templates, 10 keys
- 400 days of history; CSV and Parquet export; nightly reports with
  per-strategy comparison
- 12 seats, operator role; e-mail plus shared Telegram channel, 8 business
  hours
- Extra seats available
**CTA:** Choose Desk

### Institution — annual, quoted
{{price.institution.annual}} (from)
For a desk that needs a named contact, streaming data, scheduled exports
and multi-year retention.
- All venues, plus venue requests
- 250 active rules (soft limit); unlimited templates
- 2-second refresh, the collector floor
- All channels, multiple Telegram destinations, 40,000 alerts per day
- Auto-paper: all five strategies; 600 open simulated positions; 25 ledgers
- Read + write API, 50 keys, streaming WebSocket
- 3 years of history plus export to your object storage; scheduled exports;
  custom report cadence
- 40 seats, custom roles; named contact, 4 business hours, quarterly review
- Pilot by agreement; invoiced annually
**CTA:** Request a quote

## Comparison table

[Render packages.md §2 rows: Venues; Triangular venues; Active rules;
Templates; Refresh; Channels; Alerts per day; Auto-paper strategies; Open
simulated positions; Ledgers; API; History; Export; Reports; Seats; Roles;
Support. Final row, all columns: **Live execution — not offered.**]

## What every package includes

- Fees subtracted on every leg; verification status shown per venue.
- Data-age gate on every comparison.
- Decimal arithmetic throughout.
- "Simulated" label on every paper figure, export, API response and alert.
- The risk disclosure, linked from every spread and result.
- A free export of your personal data, independent of the data-export
  feature.

## Evidence

> **Evidence status** — source: `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3`,
> generated 2026-08-27: **0 cycles executed**, no profitability statement.
> No package description on this page cites a return, hit rate or spread
> size, because no filed report supports one. **No evidence yet.**

## Billing FAQ

**Who charges me?** Paddle, as merchant of record. Prices shown are
Paddle's localised previews and include tax where required.

**When does an upgrade apply?** When Paddle confirms payment — usually
seconds. The console shows "pending" until then.

**When does a downgrade apply?** At the end of the current period. You
keep current limits until then; afterwards data above the new limits is
read-only for 30 days, then removed.

**Is there a money-back period?** Yes: the first paid period of a new
subscription, within 14 days, once per organisation. Consumers in the EU
and UK also have statutory rights; see the [Refund Policy](/legal/refund).

**What happens when the trial ends?** The organisation moves to Watch.
Rules above 2 are paused, simulated positions are closed on paper, and
data above 24 hours is kept 30 days for export.

**Can any package place real orders?** No. Live execution is disabled in
the software for every package and is not an add-on or a contract term.

**Are support times contractual?** Response targets are targets. Desk and
Institution customers receive a separate SLA annex; venue data feeds are
excluded from any availability commitment.

---

{{brand}} is a market-data, analytics and simulation tool. It does not
execute trades, hold funds, hold exchange keys or give advice.
[Risk Disclosure](/legal/risk-disclosure)
