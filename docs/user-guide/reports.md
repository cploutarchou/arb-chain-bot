# Nightly paper reports

Two report systems exist; do not confuse them.

- **Reports** (console page *Analysis → Reports*, `GET /api/v1/reports`)
  are the engine's daily/weekly operational reports for the triangular
  engine: system and exchange health, scanner counts, paper cycles, PnL by
  asset, slippage, incidents. OPERATOR or ADMIN can generate one on demand;
  CSV and JSON download.
- **Screener paper reports** (this page) are the nightly evidence reports
  for the Scanner Suite's automatic paper execution:
  `GET /api/v1/screener/reports`, `GET /api/v1/screener/reports/{id}`
  (`screener:view`), `POST /api/v1/screener/reports/run` (`screener:config`,
  CSRF, audited). The console lists them under Scanner Suite → Screener
  Reports (detail view renders the stored markdown; "Run now" is
  ADMIN-only); the same content is on disk and in the Telegram summary.

## When and what

- Scheduled every day at **00:05 UTC**, once the previous UTC day is
  complete and its last funding settlement is booked. A missed slot (process
  down) is **not** back-filled automatically: run
  `POST /screener/reports/run` for the missed day; the run is logged either
  way. `GET /screener/reports` returns `next_run_utc` and `last_run`
  (day, started, duration, reports, errors).
- Per run, for every strategy that has rules or ledger rows, and for every
  rule under it: one **day** report (previous UTC day) and one
  **cumulative** report (since the first ledger row). `rule_id` is empty on
  the per-strategy aggregate.
- Each report contains the §7 statistics table, the §8 gate checklist, a
  fixed model statement, notes listing what could not be computed, and the
  same footer every alert carries.

## Where files land

```
<ARB_RECORDING_DIR>/screener-reports/<YYYY-MM-DD>/<strategy>[__<rule>]__<day|cumulative>.md
<ARB_RECORDING_DIR>/screener-reports/<YYYY-MM-DD>/<strategy>[__<rule>]__<day|cumulative>.json
```

`ARB_RECORDING_DIR` is the recordings volume (`docs/deployment.md` §3d).
When no directory is configured the `files` fields are empty and the
report exists only as a `screener_reports` row (migration 000012), which
the API serves with its markdown (`md`) and payload.

One Telegram message per run, when the notification service is running:
"Paper report for <day> UTC (previous day) and cumulative", then one line
per strategy and period — `n`, net in quote, `gate x/8 pass` — or "No
strategy has rules or ledger rows yet: nothing measured", an error count
if any, and the fixed footer.

## Statistics (per report)

`n`, `matched_pairs`, `net_pnl_quote`, `pnl_after_rebalance`,
`matched_pair_net`, `unwind_cost_quote`, `partial_leg_pnl_quote`,
`conservative_net_quote`, `fees_quote`, `net_bps_mean` / `median`,
`hit_rate` with a Wilson 95 % interval and win count, lifetime mean /
median, `max_drawdown_quote` / `frac` over allocated paper capital
(realised samples only), inventory drift per lane, `skipped{reason}`,
`funding_rows` / `funding_net_quote`, realised slippage mean / p95 versus
the allowance, `concentration` (largest single day's share of net),
`days` / `weekend_days`, and — when n reaches the floor and there are at
least two days — the Wilcoxon statistic and the bootstrap CI.
`n_regime` reads "not computed": the screener does not store the
calm/volatile regime classification yet.

All money math is decimal end to end; the one square root in the test
statistics is a Newton iteration on decimals.

## The gate checklist, item by item

Each of the eight items is `pass` or `fail` with a reason. There is no
third state: an item the ledger cannot evidence **fails** with a reason
beginning `no evidence yet`. The checklist is evaluated on the cumulative
statistics; the daily report prints it too, where every duration and
sample item fails by construction.

| # | Item | What can pass today |
|---|---|---|
| 1 | Duration and coverage | always fails: days with samples and weekend days are counted, but calm/volatile day counts are not computed (no stored regime classification) |
| 2 | Minimum sample | always fails: `n`, matched pairs (spot) or funding settlements (perp kinds) are compared with the floors, but the per-regime floor cannot be evaluated |
| 3 | Positive net in every regime | always fails: the conservative net is reported and compared, but the per-regime split cannot be evaluated |
| 4 | Statistical test | **can pass** once `n` reaches the floor and ≥ 2 days exist: Wilcoxon rejects H₀ and the bootstrap CI lower bound is > 0 |
| 5 | Stress grid | always fails: the §80 stress grid is a re-simulation over the recorded window, run separately and filed under `docs/campaigns/` |
| 6 | Drawdown and concentration | **can pass** once `n` reaches the floor: drawdown fraction ≤ 0.05 (needs allocated paper capital > 0), concentration ≤ 0.40, largest loss ≤ 10× median win |
| 7 | Model honesty | always fails: the slippage sub-check (p95 ≤ allowance) is evaluated and printed, but "no unverified fee in the executed venue set" is a manual check against `docs/research/screener-endpoints.md` that the report cannot evidence |
| 8 | Non-statistical items | always fails: security review, decision record and the human-merged code change are manual |

So the best a report can show today is `2 / 8 pass`. That is by design:
the checklist never softens a missing measurement into a pass, and the
items that stay red name what a human still has to do. Every report ends
with "Failing any item means LIVE stays disabled."

## Publishing a number

A figure from a screener report may be quoted outside the console only
after the report has been filed under `docs/campaigns/<strategy>/` and is
cited by path, verbatim, with its fees and gate verdicts. Where no such
report supports a claim, copy says "no evidence yet". As of 2026-08-27 the
only campaign on file (`docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3/`)
executed no cycles and makes no profitability statement.

---

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
