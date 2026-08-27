# Alert rules

Console page **Scanner Suite → Alert Rules**; API
`GET/POST /api/v1/screener/rules`, `PUT/DELETE /api/v1/screener/rules/{id}`
(mutations: `screener:config`, CSRF, audited), `GET /api/v1/screener/events`.

A rule is a filter you configure. When a lane satisfies it for long enough,
the evaluator opens an **event** (visible in the Events table), optionally
pushes a Telegram message, and — only if the rule opts in — hands the
signal to [auto-paper](auto-paper.md). Rules are measurements of your
filters; they contain no buy or sell wording, and no rule templates are
shipped that pick assets for you.

## Rule fields

| Field (console) | Wire | Notes |
|---|---|---|
| Name | `name` | 1–100 characters |
| Kind | `kind` | `spread` (cross-venue spot), `carry` (spot long + perp short on one venue), `basis` (funding-driven entry on one venue; runs the `funding_harvest` model by default) |
| Min spread (bps) | `min_spread_bps` | required for `spread`; compared with the **executable** edge (net of fees, minus slippage allowance per leg and buffer — defaults 2 bps and 5 bps), not the raw net |
| Min carry APR (%) | `min_carry_apr` | required for `carry` and `basis`; the wire value is a fraction (0.07 = 7 %) and the console converts exactly both ways |
| Min liquidity (quote) | `min_liquidity_quote` | top-of-book liquidity floor; unknown liquidity never passes |
| Min lifetime (s) | `min_lifetime_s` | the lane must stay above threshold continuously this long before an event opens |
| Cooldown (s) | `cooldown_s` | see below; default 300 in the form |
| Buy venues / Sell venues | `buy_venues`, `sell_venues` | chips; empty = any enabled venue |
| Quotes | `quotes` | comma list, exact (USDT and USDC are different quotes); default USDT |
| Bases allow / deny | `bases_allow`, `bases_deny` | comma lists |
| Enabled | `enabled` | a disabled rule is kept but not evaluated |
| Push to Telegram | `telegram` | legacy single-channel flag, kept for rules saved before channels existed; equivalent to `channels: ["telegram"]` (see below) |
| Alert channels | `channels` | array of `telegram`, `email`, `webhook`; each is gated by the organisation's `alerts.channels` package entitlement at open time. Empty `channels` with `telegram: true` behaves exactly like `channels: ["telegram"]` — the two fields are never combined |
| E-mail address | `email_to` | required and validated when `channels` includes `email` |
| Webhook URL | `webhook_url` | required, `http(s)://` only, when `channels` includes `webhook` |
| Webhook secret | `webhook_secret` | required (≥ 16 characters) when `channels` includes `webhook`; **write-only** — never returned by `GET`/list/create/update. Omitting it on an update keeps the existing secret; sending a new value rotates it |
| Automatic PAPER execution | `auto_paper` | the form labels it "live trading is disabled by design"; needs `paper_size_quote > 0` |
| Paper size (quote) | `paper_size_quote` | per-execution notional; clamped by the package's `auto_paper.max_size_quote` |

Advanced model inputs (`params`: slippage allowance, depth haircut, buffer,
step size, drift cap, carry entry/exit thresholds, margin-stop fraction,
basis-stop, maintenance margin rate, funding-strategy floors) are accepted
on the API and documented in `docs/design/strategy-models.md` §1–§5. The
the console exposes `slip_bps`, `buffer_bps`, `max_hold_h` and `mmr` under
"Advanced model inputs" (empty means the documented default; `mmr` is never
pre-filled), and preserves any other stored `params` when you edit a rule. One default matters for perp
rules: the maintenance margin rate (`params.mmr`) is **not** inferred from
any venue, so a `carry`/`basis` rule with auto-paper on skips every entry
with `MMR_UNKNOWN` until you set it through the API.

## How an event opens and closes

Every poll, for each rule and each lane in its filters, the evaluator
computes the lane's score (executable edge for spot; edge or funding
figure for perp kinds), checks the data-age gate and the liquidity and
guard exclusions, and keeps a lifetime tracker per lane:

1. The lane's score first exceeds the threshold → `first_seen` is stamped.
2. It stays above the threshold for `min_lifetime_s` → an event opens
   (`opened_at`, `lifetime_s`, `peak_net_bps`); every channel the rule
   effectively asks for (`channels`, or `["telegram"]` from the legacy
   flag) is dispatched, each independently gated by the organisation's
   `alerts.channels` entitlement; auto-paper runs if enabled.
3. The score drops below the threshold, or the lane fails a gate → the
   event closes with its total lifetime and peak; a close message is
   sent, best-effort, on the same channels (the entitlement gate and the
   `alerts.per_day` quota were already spent when the event opened, so
   closing does not spend them again).

### How channel delivery is dispatched and recorded

Telegram is delivered synchronously (the notification router never
blocks). E-mail and webhook are handed to a bounded worker pool — a
slow SMTP server or a webhook mid-retry never stalls the poll loop — so
their outcome is not yet known when the event is stored. Every event
carries a `delivered` map, one entry per requested channel:

```json
"delivered": {
  "telegram": {"status": "sent", "at": "…"},
  "email":    {"status": "sent", "at": "…"},
  "webhook":  {"status": "failed", "reason": "webhook endpoint returned 500", "at": "…"}
}
```

`status` is one of `sent`, `failed`, `skipped` (the organisation is not
entitled to that channel), or `pending` (email/webhook only, briefly,
between the event being stored and the worker finishing). `reason` is
set for `failed` and `skipped`.

### E-mail

Sent over SMTP with STARTTLS (or implicit TLS for `smtps://`), configured
once for the whole platform via the `smtp_url` secret
(`smtp://user:pass@host:587`) — see `docs/design/billing.md` §4. A rule
with `channels` including `email` but no configured `smtp_url` records
`delivered.email = {"status": "failed", "reason": "channel not
configured on this process"}`; the alert itself, and every other
channel, is unaffected.

### Webhook

`POST`ed as JSON to `webhook_url`:

```json
{"event_id": "…", "rule_id": "…", "kind": "spread", "title": "…", "body": "…", "at": "…"}
```

with a header proving it came from this platform:

```
X-Arb-Signature: ts=<unix-seconds>;h1=<hex HMAC-SHA256 of "<ts>:<raw body>" keyed by webhook_secret>
```

— the same shape `internal/billing/paddle` uses for its own outbound
webhook signatures. Verify by recomputing the HMAC over `"<ts>:<body>"`
with your `webhook_secret` and comparing in constant time; also check
that `ts` is recent (a few minutes) to reject replays.

Delivery: 5-second timeout per attempt, up to 3 retries with linear
backoff, no redirects followed (a redirecting endpoint is treated as a
failure), and the destination is refused outright — before any network
attempt — if it resolves to a loopback, private (RFC 1918), link-local
(this covers the `169.254.169.254` cloud metadata endpoint), CGNAT
(`100.64.0.0/10`), unspecified or multicast address. Only `http://` and
`https://` targets are accepted.

### Cooldown

`cooldown_s` is per rule and per lane: after an event opens on a lane, no
new event opens on that same lane until `cooldown_s` have passed since the
last open, even if the lane closes and re-qualifies in between. Dedup is a
consequence: a lane that flickers around the threshold produces one event
per cooldown window, not one per poll. The lowest cooldown a package may
set is `alerts.min_cooldown_s` (300 s on Watch, down to 5 s on
Institution); a smaller value is rejected with `403 entitlement_exceeded`.

## What an alert says

Alert text is generated by the backend and cannot be edited. It is a
measurement, never advice:

- Legs are described as `ask@venue` and `bid@venue`; there are no buy or
  sell verbs.
- A spot alert carries the pair, both prices, gross / net-of-fees /
  after-slip-and-buffer bps, top-of-book liquidity with both sizes, both
  taker fees, both data ages and the lifetime.
- A carry alert carries perp bid vs spot ask and the basis, the predicted
  funding per interval and the interval, expected funding over the hold,
  round-trip fees and the resulting edge, liquidity, ages and lifetime.
- A funding-strategy alert carries predicted funding, the settled mean over
  the lookback, basis, round-trip costs and the breakeven interval count.
- The close message carries the lifetime, the peak and the current score.
- Every message ends with a fixed footer stating that the figures are a
  measurement of public quotes net of configured taker fees, model-based
  and hypothetical, not a recommendation to trade, that nothing is assured,
  and that simulated or past figures are not indicative of future results.
  Simulated results are labelled as such.

## Events table

`GET /screener/events?rule_id=&limit=` — the console shows the latest 100,
for all rules or the selected one: opened, closed (or `open`), lifetime,
pair, buy → sell venues, peak net bps, whether Telegram was sent, and the
paper execution id when auto-paper executed. Deleting a rule stops
evaluation; past events are kept. Rows older than the package's history
depth (`history.retention_days`) are not returned.

## Channels by package

| Channel | Status | Packages |
|---|---|---|
| Web (Events table) | built | all |
| Telegram | built (notification service, allowlist, one destination; Institution up to five) | Signal and above |
| E-mail | built (SMTP, `smtp_url` secret) | Operator and above |
| Webhook | built (signed, retried, SSRF-hardened) | Desk and above |

A rule may request a channel its organisation is not entitled to (e.g. a
Signal-tier rule with `channels: ["webhook"]`); this is refused at
create/update time with `403 entitlement_exceeded` (`data.key =
"alerts.channels"`), the same way every other package limit in this
document is enforced.

Alerts per day are also a package limit (`alerts.per_day`); when exceeded
the event is still stored and every channel is suppressed and counted as
`skipped: ALERTS_PER_DAY`. The counter is wired into the evaluator per
organisation; a refusal opens no event and spends no cooldown.
Concurrent enabled rules are capped by `rules.max_active`; a `POST` beyond
the cap answers `403 entitlement_exceeded` with the key and limit.

Telegram chat identifiers are personal data: they are configured in
platform settings, not in rules, and are not printed in alert bodies.
E-mail and webhook destinations (`email_to`, `webhook_url`) are set per
rule by the organisation that owns it.

## Client API access

Operator and above may read rules/events over the client API
(`docs/design/packages.md` §3.1 `api.*`); Desk and above may also create,
update and delete them. See `docs/design/billing.md` §1.6 for API-key
management, scopes and rate limits — the same `/api/v1/screener/rules`
and `/api/v1/screener/templates` endpoints this page documents, reached
with `Authorization: Bearer <key>` instead of a browser session.

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
