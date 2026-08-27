# User guide — Scanner Suite

Status: written 2026-08-27 against branch `scanner-suite`. Everything on
these pages describes what is built and tested in this branch; anything
that is not yet built is marked **planned**. Numbers in this guide are
model inputs or worked examples, never results. Results live only in
`docs/campaigns/`, and as of this date no report there shows a positive
simulated result for any strategy ("no evidence yet").

The product measures public quotes on crypto venues, applies fee models,
sends alerts for conditions you configured, and records what a simulated
order would have done. It does not trade, hold funds, hold exchange keys or
advise. Live execution is disabled in code (`ErrLiveTradingDisabled`) and
is not a setting, a package or an add-on.

## Pages

| Page | What it covers |
|---|---|
| [Getting started](getting-started.md) | Sign-in, roles, the risk acknowledgement, first-run checklist, simulated balances |
| [Screener](screener.md) | Cross-venue spot spreads: columns, the net-of-fees formula, suspect and unknown-liquidity exclusions, saved templates, data age |
| [Perpetuals and funding](perpetuals-and-funding.md) | Basis, carry APR (30-day hold assumption), funding intervals per venue, the information-only notice |
| [Calculator](calculator.md) | Sizing one measured spread with your own fees and a transfer fee |
| [Alert rules](alert-rules.md) | Rule fields, cooldown, what an alert says, channels by package |
| [Auto-paper](auto-paper.md) | Automatic simulated execution: what is and is not simulated, skip reasons, the per-rule evidence table, the production gate |
| [Reports](reports.md) | Nightly paper reports, where the files land, what the gate checklist means (file to be renamed `reports.md`) |
| [Packages and billing](packages-and-billing.md) | Packages by capability, trial, upgrades and downgrades, past-due read-only |
| [Venues](venues.md) | The ten venues, which fees are verified, what each venue cannot publish |

Operators: see also `docs/runbooks/screener-collectors.md` (rate limits,
self-healing restarts, 418/403/510 handling) and `docs/deployment.md`
§3b–§6 for the deployment and console-driven operations.

## Conventions used throughout

- **bps** = basis points; 1 bps = 0.01 %. Fees, spreads and edges are in
  bps. Funding rates are per-interval fractions exactly as the venue
  publishes them; annualised carry is a fraction (0.1095 = 10.95 % per
  year) on the wire and display only.
- **Decimal strings.** Every price, size, fee, rate and PnL is a decimal
  string on the wire and in the console; the console never recomputes a
  money figure, it renders the backend's own string.
- **Executable sides.** Buy at ask, sell at bid, always. Mid prices appear
  only in the basis display and the asset-identity guard, never in a PnL.
- **Net of taker fees.** Every spread and carry number is net of the taker
  fee in the venue fee table (Settings → Scanner Suite). Which venue fees
  are verified against an official fee page is listed in [Venues](venues.md).
- **No-transfer model.** Cross-venue numbers assume inventory already sits
  on both venues. Transfers are not modelled unless you type a transfer
  fee into the calculator.
- **Data age.** Every row carries the age of the quote behind it. Rows
  older than 3× the poll interval are dimmed and their age cell reads
  `STALE`; 1–3× is highlighted as a warning.
- **Simulated.** Paper balances, paper fills and paper PnL are simulated
  and labelled so in every table, export, API response (`mode: paper`),
  alert and report.

## Words we do not use

Product copy, alerts, reports and this guide never describe an outcome as
assured, never describe any strategy as free of risk or as an income, and
never cite a return, hit rate or spread size that is not quoted verbatim
from a report in `docs/campaigns/`. The copy lint's banned-word list
(compliance review item #6) applies to these pages too; the only
permitted occurrence of the fourth banned word is the strategy identifier
`funding_harvest`.
