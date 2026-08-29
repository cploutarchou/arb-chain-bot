# Packages and billing

Packages differ in **capacity** — venues, rules, refresh interval, paper
positions, history, seats, support. They never differ in what the product
is: market data, analytics, alerts and simulated execution. No package, at
any price, places orders on an exchange, holds funds or takes exchange API
keys, and `execution.live` is `false` in every package document by
construction. It is not an entitlement, an add-on or a contract term.

Prices are not printed here. They come from Paddle price previews at
checkout so tax and currency are right, and are referenced in copy as
`{{price.signal.month}}`, `{{price.signal.year}}`,
`{{price.operator.month}}`, `{{price.operator.year}}`,
`{{price.desk.month}}`, `{{price.desk.year}}`,
`{{price.institution.year}}` and `{{price.seat.month}}`. Annual billing is
twelve months for the price of ten. Package copy describes capabilities
and limits only — no return, hit rate or spread size, because no report in
`docs/campaigns/` supports one ("no evidence yet").

## Packages by capability

| | Watch | Signal | Operator | Desk | Institution |
|---|---|---|---|---|---|
| Price | free | {{price.signal.month}} / {{price.signal.year}} | {{price.operator.month}} / {{price.operator.year}} | {{price.desk.month}} / {{price.desk.year}} | from {{price.institution.year}}, annual, quoted |
| Screener and perps venues | 3, fixed (Binance, OKX, Bybit) | 6 (Tier-1) | Tier-1 + Tier-2 | all CEX venues | all, plus venue requests |
| Triangular engine venues | 1 | 2 | 4 | all supported | all |
| Concurrent alert rules | 2 | 8 | 25 | 80 | 250 (soft) |
| Saved screener templates | 3 | 10 | 40 | unlimited | unlimited |
| Lowest screener refresh | 30 s | 10 s | 5 s | 3 s | 2 s (collector floor) |
| Alert channels | web | web + Telegram | + e-mail (**planned**) | + webhook (**planned**) | + webhook, up to 5 Telegram destinations |
| Alerts per day | 20 | 200 | 1 500 | 8 000 | 40 000 |
| Lowest alert cooldown | 300 s | 60 s | 30 s | 10 s | 5 s |
| Rule kinds | spread, basis | + carry | + funding, triangular | same | same |
| Auto-paper strategies | none (manual paper only) | cross-venue spot | + carry, triangular | all five (adds futures–futures, `funding_harvest`) | all five |
| Auto-paper open positions | 0 | 5 | 30 | 150 | 600 |
| Per-execution paper size cap (quote) | 0 | 5 000 | 25 000 | 100 000 | 1 000 000 |
| Paper ledgers | 1 | 1 | 3 | 10 | 25 |
| Client API | no | no | read, 60 req/min, 2 keys (**planned**) | read + rule/template writes, 300 req/min, 10 keys (**planned**) | read + write, 1 200 req/min, 50 keys, streaming (**planned**) |
| History depth | 24 h | 14 days | 90 days | 400 days | 3 years + object-storage export |
| Data export | no | CSV, 14 days | CSV, 90 days | CSV + Parquet | + scheduled exports |
| Evidence reports | public samples | own rules, weekly | own rules, nightly | nightly + per-strategy comparison | nightly + custom cadence |
| Seats | 1 | 1 | 3 | 12 | 40 (more on quote) |
| Roles | owner | owner | owner, admin, viewer | + operator | + custom names |
| Support | community docs | e-mail, 2 business days | e-mail, 1 business day | e-mail + shared Telegram, 8 business hours | named contact, 4 business hours, quarterly review |

Futures–futures and triangular auto-paper strategies are listed in the
entitlement schema; of the five, `cross_venue_spot`, `carry` and
`funding_harvest` run in the screener executor today, and triangular runs
in the existing engine. Futures–futures execution is **planned**.

Watch exists so the marketing site can show real, delayed numbers instead
of screenshots; its 30 s refresh and web-only alerts make it a demo, not a
signal feed. Marketing figures from Watch are labelled as a live screener
sample, never as campaign results.

## How limits are enforced

Every limit is enforced server-side; the console only reads
`entitlements` from `GET /api/v1/me` (with `entitlements.status`:
subscription state, read-only flag, effective package, trial end).

| Limit | On breach |
|---|---|
| Venues (`screener_max`, `screener_fixed`) | `403 entitlement_exceeded` on rule and settings writes; rules naming a venue you lose are paused, not deleted |
| Rules (`max_active`, `kinds`, `min_refresh_s`, `templates_max`) | `403`; the evaluator loads the first N enabled rules by creation time and flags the rest `paused_by_entitlement` |
| Alert channel, cooldown, destinations | `403` at rule validation |
| Alerts per day | event stored, push suppressed, counted `skipped: quota` (dispatcher wiring **planned**) |
| Auto-paper strategy, size cap, open positions | rule fires as alert only; execution `skipped: entitlement`; size clamped (decimal compare) |
| History depth | older rows invisible through the API; purge job with 30-day grace **planned** |
| Seats, roles | `403` on invite / role change; seat suspension on downgrade **planned** |
| Live execution | code, not config; a schema validator also rejects any entitlement document with `live: true` |

## Trial

Every new organisation starts a 14-day Operator trial, no card, one per
organisation. On day 15 without a subscription the organisation drops to
Watch: rules beyond two are paused, paper positions are closed on paper,
data is retained 30 days at trial depth and then trimmed to Watch depth.
Today the platform operator creates organisations; self-service sign-up is
**planned** (T-085).

## Upgrades, downgrades, cancellation

Paddle is the source of truth for money; our subscription row is a mirror.
Entitlements change only on a verified, idempotent webhook — never on the
checkout redirect — so the console shows "pending" until it lands
(console rendering of that state is **planned**; the API already reports
it).

| Change | Timing | Charge | Entitlements |
|---|---|---|---|
| Trial → paid | immediate at checkout | full period; trial days not credited | on `subscription.activated` |
| Upgrade (same interval, or monthly → annual) | immediate | prorated immediately; period end unchanged (monthly → annual starts a new annual period) | on `subscription.updated`, within seconds |
| Downgrade (tier, or annual → monthly) | takes effect at period end | no refund, no credit; full new price at renewal | current package kept until `current_period_end`, then breach handling with 30-day data grace |
| Downgrade within 72 h of an upgrade | immediate | unused prorated amount refunded as Paddle credit | immediate |
| Cancel | period end (immediate only via support) | no refund except the money-back below | Watch at period end; 30-day data grace; export offered on the cancel screen |
| Money-back | first paid period of a **new** subscription, within 14 days, once per organisation | full refund | Watch immediately |
| Extra seats (Desk, Institution) | prorated | {{price.seat.month}} per seat per month | `seats.max` += n |
| Institution | annual, invoiced, net 30 | per quote | contract-specific override document, still validated against the schema |

Every update is sent with `on_payment_failure: prevent_change`. Card data
never reaches this platform: checkout runs in Paddle's overlay and payment
methods live in Paddle's portal (`GET /api/v1/billing/portal`).

## Past due — the read-only risk

When a payment fails Paddle runs dunning for 21 days. The organisation
keeps full entitlements for **7 days** from `past_due_since`, then becomes
**read-only** until paid: alerts off (per-day quota 0), auto-paper off, API
read-only. Open paper positions stop being managed by rules while
read-only. If dunning fails the organisation drops to Watch. The
`past_due_since` timestamp is set once and survives retries, so repeated
failure webhooks do not restart the 7-day clock.

## Affiliates

An affiliate ledger with decimal accrual (20 % first 12 months, then 10 %;
Institution 8 % first year), 45-day maturation, refund reversal and
self-referral = zero exists and is wired to completed transactions.
Enrolment, the maturation/payout job, the payouts report and automated
fraud checks are **planned**. Affiliate material may not cite a return
unless it quotes a `docs/campaigns/` report verbatim.

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
