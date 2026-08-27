---
title: Alert templates (Telegram, e-mail, web inbox, webhook)
status: DRAFT — compliance-reviewer sign-off required before publish
drafted: 2026-08-27
owner: content-copywriter
constraints: review-2026-08-27 items 5, 7, 10, 21; packages.md §7 ("simulated" on every paper figure, "mode": "paper")
rules: measurement-only wording; no buy/sell verbs; no asset-picking; every paper figure prefixed "simulated"; fixed footer; no emojis; chat ids never appear in message bodies or logs
---

<!-- Placeholders are {snake_case}. Numeric placeholders are rendered by
     the dispatcher from decimal values and are rounded only at render.
     Verbs allowed for describing the event: measured, observed, crossed,
     met, fell below, cleared, settled, recorded, closed, paused, skipped.
     Verbs never used: buy, sell, long, short, enter, exit, take, grab,
     act, execute (except "simulated execution" as a noun phrase). -->

## Footers (fixed, appended verbatim)

### F1 — measurement footer (every alert)

```
Measurement, not a recommendation. Figures are public quotes at the time
shown, net of modelled taker fees; data age is printed. {brand} does not
trade, hold funds or advise. Risk disclosure: {risk_url}
```

### F2 — hypothetical-performance footer (every alert that carries a paper figure)

```
SIMULATED. Paper result from modelled fees and top-of-book fills; no order
was sent to any venue. Simulated results have inherent limitations: they
are prepared with hindsight, exclude transfers between venues, assume
withdrawal availability, model perpetual legs at one-times notional with
a hard stop, and do not reflect real liquidity, venue outages or the
effect of one's own orders. No account has traded this result and no
representation is made that any account will achieve similar results.
{brand} does not trade, hold funds or advise. Risk disclosure: {risk_url}
```

Telegram: F1 or F2 is the last block of every message; never truncated.
E-mail: F1/F2 in the body above the unsubscribe line, not only in the
signature. Webhook: `"footer"` field carries the same text; `"mode"` is
`"paper"` on any payload with a paper figure.

---

## A. Cross-venue spot

### A1 — spread rule met (Telegram)

```
{brand} · rule "{rule_name}" · {org_name}

Spread measured: {base}/{quote}
  ask {venue_buy_side}: {ask_price}  (depth {ask_qty} {base})
  bid {venue_sell_side}: {bid_price}  (depth {bid_qty} {base})
  gross {gross_bps} bps · net of fees {net_bps} bps
  fees modelled: {venue_buy_side} {fee_buy_bps} bps ({fee_status_buy}),
                 {venue_sell_side} {fee_sell_bps} bps ({fee_status_sell})
  data age: {age_a_ms} ms / {age_b_ms} ms
  threshold: {threshold_bps} bps · cool-down {cooldown_s} s
  {utc_time}

Transfers not modelled. Withdrawal status unknown.
Console: {event_url}

[F1]
```

### A2 — spread rule met, simulated execution attached (Telegram)

```
{brand} · rule "{rule_name}" · {org_name}

Spread measured: {base}/{quote}
  ask {venue_buy_side}: {ask_price} · bid {venue_sell_side}: {bid_price}
  net of fees {net_bps} bps · data age {age_a_ms}/{age_b_ms} ms
  {utc_time}

SIMULATED execution (ledger "{ledger_name}"):
  size {size_quote} {quote} (simulated)
  fill {venue_buy_side}: {fill_buy} · fill {venue_sell_side}: {fill_sell}
  realised slippage {slip_bps} bps vs allowance {slip_allow_bps} bps
  simulated net: {sim_net_bps} bps of deployed · {sim_net_quote} {quote}
  status: {exec_status}   (ALL_FILLED / PARTIAL / REJECTED:{reason})

Console: {event_url}

[F2]
```

### A3 — execution skipped

```
{brand} · rule "{rule_name}" · {org_name}

Spread measured: {base}/{quote} · net {net_bps} bps · {utc_time}
Simulated execution skipped: {skip_reason}
  (DATA_AGE / DEPTH / ENTITLEMENT / POSITION_OPEN / QUOTA / SIZE_CAP)
Console: {event_url}

[F1]
```

## B. Perpetuals

Every perpetuals template begins with the information-only line.

### B1 — basis threshold met (spot vs perpetual, one venue)

```
{brand} · rule "{rule_name}" · {org_name}
Information only. Derivatives availability depends on your country and venue.

Basis measured: {venue} {symbol}
  perp bid/ask {perp_bid}/{perp_ask} · spot bid/ask {spot_bid}/{spot_ask}
  basis {basis_bps} bps (executable sides) · net of fees {net_bps} bps
  funding {funding_rate} per {interval_h} h (settled) ·
  predicted next {predicted_rate} (predicted) · settles {next_funding_utc}
  data age {age_ms} ms · {utc_time}
Console: {event_url}

[F1]
```

### B2 — funding threshold met

```
{brand} · rule "{rule_name}" · {org_name}
Information only. Derivatives availability depends on your country and venue.

Funding measured: {venue} {symbol}
  settled rate {funding_rate} per {interval_h} h ·
  persisted {n_intervals} intervals above {threshold_rate}
  predicted next {predicted_rate} (predicted, not settled)
  next settlement {next_funding_utc} · data age {age_ms} ms
Console: {event_url}

[F1]
```

### B3 — simulated carry position opened / closed

```
{brand} · rule "{rule_name}" · {org_name}
Information only. Derivatives availability depends on your country and venue.

SIMULATED carry position {opened|closed}: {venue} {symbol}
  ledger "{ledger_name}" · notional {notional_quote} {quote} (simulated, one-times)
  entry basis {entry_bps} bps{ · exit basis {exit_bps} bps}
  funding booked: {n_settlements} settlements, {funding_total_quote} {quote}
  {close_reason}   (TARGET / HARD_STOP / RULE_DISABLED / DATA_AGE)
  simulated net incl. unwind: {sim_net_bps} bps of deployed · {sim_net_quote} {quote}
Console: {event_url}

[F2]
```

### B4 — simulated funding settlement booked

```
{brand} · ledger "{ledger_name}" · {org_name}
SIMULATED funding settlement: {venue} {symbol} at {settle_utc}
  settled rate {funding_rate} · mark {mark} · amount {amount_quote} {quote}
  position simulated notional {notional_quote} {quote}
Console: {ledger_url}

[F2]
```

## C. Triangular

### C1 — cycle qualified

```
{brand} · rule "{rule_name}" · {org_name}

Cycle measured: {venue} {start_asset} → {leg1} → {leg2} → {leg3} → {start_asset}
  gross {gross_bps} bps · net of three taker fees {net_bps} bps
  size {size_start} {start_asset} · depth ok on all legs: {depth_ok}
  data age max {age_ms} ms · {utc_time}
Console: {event_url}

[F1]
```

### C2 — simulated cycle executed

```
{brand} · rule "{rule_name}" · {org_name}

SIMULATED cycle: {venue} {start_asset} → {leg1} → {leg2} → {leg3} → {start_asset}
  ledger "{ledger_name}" · input {size_start} {start_asset} (simulated)
  legs: {leg1_status} / {leg2_status} / {leg3_status}
  status {cycle_status}   (ALL_FILLED / PARTIAL / REJECTED)
  fees paid (simulated): {fees_detail}
  realised slippage {slip_bps} bps vs allowance {slip_allow_bps} bps
  simulated net: {sim_net_bps} bps of input · {sim_net_start} {start_asset}
Console: {event_url}

[F2]
```

## D. Ledger and report notifications

### D1 — nightly report filed

```
{brand} · report filed · {org_name}
Strategy {strategy} · rule set "{rule_set}" · window {window_from}–{window_to} UTC
  samples {n_samples} · regime days calm {d_calm} / volatile {d_vol} / weekend {d_we}
  simulated net (conservative figure): {net_bps} bps of deployed
  realised slippage p95 {slip_p95_bps} bps vs allowance {slip_allow_bps} bps
  verdict: {verdict_line}
Report: {report_url}

[F2]
```

### D2 — simulated position closed by hard stop

```
{brand} · ledger "{ledger_name}" · {org_name}
SIMULATED position closed: HARD_STOP · {venue} {symbol}
  adverse move {move_bps} bps · simulated net {sim_net_bps} bps of deployed
Console: {ledger_url}

[F2]
```

## E. Operational

### E1 — rule paused

```
{brand} · rule "{rule_name}" paused · {org_name}
Reason: {pause_reason}
  (ENTITLEMENT: above package rule limit / VENUE_DISABLED: venue not in package /
   OWNER_ACTION / PAST_DUE)
Nothing was deleted. Manage: {rules_url}

[F1]
```

### E2 — daily alert quota reached

```
{brand} · {org_name}
Daily alert quota reached ({per_day}). Further events today are stored in
the web inbox and marked "skipped: quota"; no push is sent.
Inbox: {inbox_url}

[F1]
```

### E3 — channel downgraded

```
{brand} · {org_name}
Rule "{rule_name}" is routed to {channel}, which is not included in your
package. Alerts are delivered to the web inbox instead.
Inbox: {inbox_url}

[F1]
```

### E4 — venue data stale

```
{brand} · {org_name}
{venue} quotes stale for {stale_s} s. Rules on {venue} are skipping with
reason DATA_AGE until fresh data returns. No action is required from you.
Status: {status_url}

[F1]
```

## F. E-mail subjects

```
[{brand}] {rule_name}: spread measured, net {net_bps} bps
[{brand}] {rule_name}: SIMULATED execution {exec_status}
[{brand}] {rule_name}: basis measured (information only)
[{brand}] {rule_name}: funding threshold met (information only)
[{brand}] {rule_name}: triangular cycle qualified
[{brand}] Report filed: {strategy} / {rule_set}
[{brand}] Rule paused: {rule_name}
[{brand}] Daily alert quota reached
```

## G. Webhook payload envelope

```json
{
  "brand": "{brand}",
  "org_id": "{org_id}",
  "rule_id": "{rule_id}",
  "rule_name": "{rule_name}",
  "kind": "spread | basis | funding | triangular | ledger | report | ops",
  "mode": "measurement | paper",
  "measured_at": "{utc_time}",
  "figures": { "...": "decimal strings" },
  "data_age_ms": { "...": "integers" },
  "simulated": true,
  "footer": "[F1 or F2 text]",
  "risk_disclosure_url": "{risk_url}",
  "console_url": "{event_url}"
}
```

`"simulated"` is `true` on every payload with `"mode": "paper"`. All money
and rate fields are decimal strings.
