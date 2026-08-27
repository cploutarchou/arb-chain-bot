# Scanner Suite — cross-venue screener, perpetuals/funding monitor, alerts, automatic paper execution

Status: PLANNED 2026-08-27 (T-065..T-072). Owner: console + backend.

## 0. The request, as a Claude Code command

The operator's ask, restated as the command this design implements:

```
/scanner-suite Build, inside arb-chain-bot, a product functionally equivalent to
arbitragescanner.io's arbitrage tools and run it end to end from the console:

1. Cross-exchange spot screener over Binance, OKX, Bybit, Bitget, Gate, MEXC
   (public market data only): every quoted pair on every venue, best bid/ask
   and size polled every few seconds, buy-venue → sell-venue spreads in %
   NET of both taker fees, spread lifetime, top-of-book liquidity, and
   deposit/withdraw network status where a public endpoint exists
   ("unknown" otherwise — never guessed). Filters: min spread, min
   liquidity, min lifetime, venues for buying/selling, quote asset,
   white/black lists; saved filter templates; auto-refreshing table.
2. Perpetuals & funding monitor: spot-vs-perp basis per venue and cross
   venue, current/predicted funding, funding interval, annualised carry NET
   of fees, funding history; spot+futures, futures+futures and
   funding-rate strategies as rows in one table.
3. Spreads calculator: enter size, venues, fees, transfer fee → net result.
4. Alerts: rules (spread ≥ x% for ≥ y s on venues V, funding ≥ z) pushed
   to Telegram through the existing notification service with cooldown
   and dedup; rule management from the console.
5. Automatic PAPER execution ("auto-execute the flow"): when a rule fires,
   simulate the trade with the existing paper machinery — inventory held
   on both venues (no transfers), taker fills against top-of-book with
   slippage, funding accrual every interval for carry positions, close
   on convergence or stop — booking PnL to the paper ledger, visible in
   Paper Trading / PnL. LIVE execution stays disabled by design.
6. Console layout in the arbitragescanner style: icon sidebar with
   collapsible product groups, filter panel above a dense auto-refreshing
   table, saved templates, "spreads/second" and venue-count stats, light
   and dark themes — implemented with our own components and copy, no
   copied branding, screenshots or text.
7. Manage everything from the console and the DB (venues on/off, poll
   interval, fee tables, alert rules, paper balances per venue); Claude
   Code skill + agent for this subsystem.

Rules: never enable live trading, never consume exchange API keys (the
vault's exchange group stays unreadable), never edit risk-engine or
decimal money-math code, nothing described as guaranteed; every number
shown is net of fees and labelled with its data age.
```

## 1. Honest framing

arbitragescanner.io is a signal service: it shows spreads and the
operator trades by hand. Its published 10–25 % examples come from
illiquid tokens during spikes and from spot/perp dislocations in
crashes, and it publishes no track record. Building the same views on
top of this platform is straightforward and useful — they are
measurements. What this design adds beyond that product is that every
signal is **executed on paper automatically** and scored net of fees, so
the suite produces evidence (how many alerts were tradeable, at what
size, what they returned after costs) instead of anecdotes.

Two of their product lines are out of scope: on-chain wallet tracking
("insider" search, AI similar wallets, subscriptions) and NFT flipping
are not arbitrage and do not belong in this platform. Their
"academy", affiliate and white-label offers are business features, not
software.

## 2. Architecture

```
internal/screener/
  venue/           one collector per venue: public REST bulk tickers
                   (spot + USDT-M perps + funding), instrument lists,
                   currency/chain status where public; normalised to
                   Quote{Venue, Base, Quote, Bid, BidQty, Ask, AskQty, Ts}
                   and Perp{Venue, Base, Mark, Index, Bid, Ask, Funding,
                   PredictedFunding, NextFundingAt, IntervalH, CtVal}
  poller.go        per-venue polling loop with the venue's rate limit
                   (weight gates like binance.restGate), data-age tracking
  book.go          in-memory latest-quote store keyed by (base, quote)
  spreads.go       cross-venue spread computation NET of taker fees,
                   spread lifetime (first seen → last seen), liquidity
                   (min(top-of-book qty × price)), status per side
  basis.go         spot↔perp basis and funding carry, annualised, net
  rules.go         alert rules (persisted), evaluation, cooldown/dedup →
                   notification.Service (Telegram push)
  paperexec/       automatic paper execution: strategies
                   CrossVenueSpot (inventory-on-both-sides), Carry
                   (spot long + perp short), Futures-Futures;
                   writes orders/fills/cycles through the existing paper
                   ledger with venue tags; funding accrual scheduler
  settings.go      DB-backed, versioned (internal/platform-style
                   document): venues enabled, poll_interval_s, fee table
                   per venue (spot/perp taker), min liquidity, paper
                   balances per venue, rule defaults
internal/api/screenerapi.go     read models + rule/setting mutations
                                (RBAC: screener:view OPERATOR+,
                                screener:config ADMIN; CSRF; audit;
                                parent_version)
web/src/app/{screener,perpetuals,funding,calculator,scanner-alerts,auto-paper}
migrations/000010_screener.up.sql  screener_settings (versioned),
                                   screener_rules, screener_events
                                   (alert open/close with lifetime and
                                   net spread), funding_history,
                                   paper_venue_balances
```

Data path: public REST only (bulk tickers every 2–5 s per venue —
one request per venue per tick, all symbols). WebSocket L2 stays with
the triangular engine; the screener does not need depth beyond top of
book for its own semantics, and says so in the UI ("top-of-book
liquidity"). Deposit/withdraw status: Gate has a public endpoint; the
others require keys — shown as "unknown (venue requires API key)" and
never inferred. The vault's exchange group remains unread.

## 3. Numbers shown

- Spread % = (sell_bid × (1 − fee_sell) − buy_ask × (1 + fee_buy)) /
  buy_ask, taker fees from the venue fee table (regular tier, verified
  in docs/research/screener-endpoints.md), transfer fee not included
  unless the calculator is given one; the row says "no-transfer model".
- Liquidity = min over the two sides of top-of-book qty × price, in
  quote currency. When either side's collector publishes no sizes
  (Gate's bulk ticker) the row carries `liquidity_quote: null,
  liquidity_unknown: true`; an unknown is never shown as 0 and never
  compared with a minimum. Such rows are hidden by default
  (`include_unknown_liquidity=1` shows them) and the evaluator /
  executor skip them as `LIQUIDITY_UNKNOWN`.
- Asset-identity guard (`internal/screener/guard.go`, one function for
  the table, the alert evaluator and the paper executor): a lane is
  `suspect` when the two venues' mids differ by more than
  `settings.max_plausible_spread_bps` (default 2000 = 20 %, 100..100000,
  hot) — `suspect_reason: spread_exceeds_max_plausible` — or, with ≥ 3
  venues quoting the pair, when either side's mid is more than 50 % from
  the cross-venue median — `price_deviates_from_median`. The same ticker
  on two venues is not necessarily the same asset (live 2026-08-27: VON
  gate→mexc "1 922 997 909 779 bps", TROLL, XTER); suspect rows are
  hidden by default (`include_suspect=1` shows them, flagged), have no
  lifetime, and are skipped as `SUSPECT_MISMATCH` by the evaluator and
  executor. Nothing is persisted; the verdict is recomputed per request.
- Quote assets are exact: USDT, USDC, FDUSD and USD are distinct quotes
  and are never merged; `?quote=` accepts a comma list.
- Lifetime = seconds since the spread first exceeded the row's filter
  threshold without dropping below it.
- Data age per side; rows older than 3 × poll interval are greyed.
- Carry = funding_rate × (24 / interval_h) × 365 annualised, shown with
  the raw per-interval rate; basis = (perp_mark − spot_mid) / spot_mid.

## 4. Automatic paper execution

- Opt-in per rule ("auto-paper"). Preconditions: both sides' data age ≤
  poll interval, liquidity ≥ rule minimum, paper balance on both venues.
- CrossVenueSpot: buy `size` on venue A at ask (walks only the top level;
  fills capped at askQty), sell on venue B at bid (capped at bidQty),
  both with the simulation package's slippage/latency model, fees
  deducted; position is closed immediately (it is an instantaneous
  two-leg trade with pre-positioned inventory). Rebalancing between
  venues is NOT simulated — the report shows inventory drift so the
  operator sees the hidden cost.
- Carry: open spot long + perp short at the same time; accrue funding
  each interval from the venue's settled rate (funding_history); close
  when basis ≤ close threshold or after max hold; PnL = basis capture +
  funding − 4 taker fees. Liquidation is modelled as a hard stop at the
  venue's maintenance margin with 1× notional (no leverage).
- Every execution is a paper cycle with strategy tag so PnL & Analytics
  and Reports break it down; campaign-style summary per rule: alerts,
  executed, skipped (reason), net PnL, hit rate, average lifetime.

## 5. Console

New nav group **Scanner Suite** (icons, collapsible), pages:
`/screener`, `/perpetuals`, `/funding`, `/calculator`, `/scanner-alerts`,
`/auto-paper`. Layout: stats strip (venues online, pairs tracked,
spreads/s, data age), filter card (venue chips for buy/sell, min
spread, min liquidity, min lifetime, quote asset, lists, template
save/load), dense virtualised table auto-refreshing every poll, row
expand for per-side quotes and the calculator prefilled. Light theme
added to the design system; existing pages inherit it.

## 6. Acceptance (per task)

- T-065 endpoints research: every field VERIFIED with URL + date or
  marked UNVERIFIED; fee table sourced.
- T-066 collectors: each venue returns normalised quotes for ≥ 90 % of
  its tradable spot pairs within one poll; rate limits respected under a
  30-minute soak with zero 429/418; unit tests on fixtures.
- T-067 spreads/basis: golden tests on the net-spread and carry math
  (decimal, no floats in money paths); lifetime tracking tests.
- T-068 settings + API + RBAC + audit; parent_version concurrency;
  capabilities route lists screener venues.
- T-069 console pages with e2e (filters, templates, table refresh).
- T-070 alerts: rules persisted; Telegram push with cooldown; audit.
- T-071 auto-paper: executions visible in Paper/PnL; a 24 h soak report
  (alerts → executed → net PnL) filed under docs/campaigns/screener/.
- T-072 skill + agent + deployment docs.

## 7. Wire contract (v1)

All routes under `/api/v1/screener`, JSON `{data, error}` envelope like
the rest of the API. Reads need `screener:view` (VIEWER+), mutations
`screener:config` (ADMIN) + CSRF + `parent_version` where versioned.

```
GET  /screener/status
  { venues: [{id, name, enabled, online, last_poll_at, poll_ms,
              spot_pairs, perp_contracts, rate_limited, polls, restarts, error?}],
    pairs_tracked, spreads_per_sec, poll_interval_s, updated_at }
  restarts: how many times the self-healing check replaced the venue's
  collector goroutine (no completed poll for 5 × poll_interval_s).

GET  /screener/spreads?min_spread_bps=&min_liquidity=&min_lifetime_s=
      &buy=binance,okx&sell=&quote=USDT,USDC&base=&limit=200
      &include_suspect=0|1&include_unknown_liquidity=0|1
  min_liquidity defaults to settings.min_liquidity_quote; quote is an
  exact comma list (USDT/USDC/FDUSD/USD never merged); suspect and
  unknown-liquidity lanes are excluded unless the include flag is 1.
  { rows: [{ base, quote, buy_venue, sell_venue,
             buy_ask, buy_ask_qty, sell_bid, sell_bid_qty,
             spread_bps_gross, spread_bps_net,
             liquidity_quote: "decimal" | null, liquidity_unknown: bool,
             suspect: bool, suspect_reason?: "spread_exceeds_max_plausible"|"price_deviates_from_median",
             lifetime_s, first_seen_at, buy_age_ms, sell_age_ms,
             buy_fee_bps, sell_fee_bps,
             networks: { buy_withdraw: "open|closed|unknown", sell_deposit: "...", reason? } }],
    total, excluded: { suspect, liquidity_unknown },
    filters: { min_liquidity, include_suspect, include_unknown_liquidity, max_plausible_spread_bps },
    generated_at, model: "no-transfer, top-of-book" }

GET  /screener/perpetuals?venue=&base=&min_carry_apr=&limit=
  { rows: [{ venue, base, quote, spot_mid, perp_mark, perp_index,
             basis_bps, funding_rate, predicted_funding_rate,
             funding_interval_h, next_funding_at, carry_apr_gross,
             carry_apr_net, spot_fee_bps, perp_fee_bps, age_ms }],
    generated_at }

GET  /screener/funding?base=&venues=&hours=72
  { series: [{ venue, base, points: [{at, rate}] }] }

POST /screener/calculator
  { base, quote, buy_venue, sell_venue, size_quote, transfer_fee_quote?, override_fees? }
  → { buy_ask, sell_bid, size_base, gross, fees_buy, fees_sell, transfer_fee, net, net_bps, liquidity_ok }

GET  /screener/settings            { version, created_at, settings, field_timing }
POST /screener/settings            { parent_version, settings }        (ADMIN)
  settings = { poll_interval_s, min_liquidity_quote,
               max_plausible_spread_bps,        (default 2000; 100..100000; hot)
               venues: { <id>: { enabled, spot_taker_bps, perp_taker_bps, perps_enabled } },
               paper: { balances: { <venue>: { <asset>: "decimal" } } } }

GET  /screener/rules               { rules: [Rule] }
POST /screener/rules               Rule (without id)                     (ADMIN)
PUT  /screener/rules/{id}          Rule                                  (ADMIN)
DELETE /screener/rules/{id}                                              (ADMIN)
  Rule = { id, name, enabled, kind: "spread"|"carry"|"basis",
           min_spread_bps?, min_carry_apr?, min_liquidity_quote,
           min_lifetime_s, buy_venues[], sell_venues[], quotes[],
           bases_allow[], bases_deny[], cooldown_s,
           telegram: bool, auto_paper: bool, paper_size_quote }

GET  /screener/events?rule_id=&limit=      alert history
  { events: [{ id, rule_id, kind, opened_at, closed_at?, lifetime_s,
               base, quote, buy_venue, sell_venue, peak_net_bps,
               telegram_sent, paper_execution_id? }] }

GET  /screener/auto-paper          { positions: [...], summary: { per_rule: [{rule_id, alerts, executed, skipped: {reason: n}, net_pnl_quote, hit_rate, mean_lifetime_s}] } }

GET  /screener/reports?limit=      nightly paper reports (T-078), screener:view
  { reports: [{ id, period_start, period_end, period_label: "day"|"cumulative",
                strategy, rule_id ("" = per strategy), n, net_pnl_quote,
                gate_passed, gate_total, created_at }],
    last_run?: { day, started_at, duration_ms, reports[], errors[]?, dir? },
    next_run_utc, generated_at }
GET  /screener/reports/{id}        { report: { …summary fields, payload: { window, stats (strategy-models §7),
                                     gate: [{item, name, status: "pass"|"fail", reason}], gate_passed, gate_total,
                                     generated_at, data_age_ms, model, notes[], files }, md } }
POST /screener/reports/run         generate now for the previous UTC day + cumulative   (ADMIN, CSRF, audited)
  → { run: { day, started_at, duration_ms, reports[], errors[]? } }
  Files: <ARB_RECORDING_DIR>/screener-reports/<YYYY-MM-DD>/<strategy>[__<rule>]__<day|cumulative>.{md,json};
  rows in screener_reports (migration 000012); one Telegram summary per run
  (measurement wording, fixed footer). Scheduled daily at 00:05 UTC.

GET  /screener/templates           { templates: [{id, name, filters}] }   (per user)
POST /screener/templates / DELETE /screener/templates/{id}
```
