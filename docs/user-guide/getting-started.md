# Getting started

## 1. Sign in

Open the console (`cd web && npm run build && npm start`, or the deployed
host) and sign in with email and password. The sign-in page states the
posture of the whole product: paper trading only, live execution
permanently disabled.

- The first administrator account is created at first boot from
  `ARB_ADMIN_EMAIL` / `ARB_ADMIN_PASSWORD` (`docs/deployment.md` §3d);
  those variables are ignored once any user exists. Further users are
  created under **Settings → Users & Security** by an ADMIN.
- Too many failed attempts answer "Too many attempts; wait a minute."
- Console roles: **VIEWER** (read), **OPERATOR** (read plus recording and
  campaign controls), **ADMIN** (configuration). For the Scanner Suite,
  every page is readable with `screener:view` (VIEWER and above); creating
  or editing rules, changing Scanner Suite settings and running a report
  on demand need `screener:config` (ADMIN). Saving your own screener
  filter templates needs only `screener:view`.
- Tenant organisations (Watch, Signal, Operator, Desk, Institution
  packages) have their own membership roles — OWNER, ADMIN, MEMBER,
  VIEWER — on top of the console role. See
  [Packages and billing](packages-and-billing.md).

## 2. Risk acknowledgement

Every tenant organisation must acknowledge the current version of the
Risk Disclosure before it can use protected routes.

What exists today (backend, tested):

- `GET /api/v1/me` returns `risk_ack_required` and `risk_ack_version`.
- `POST /api/v1/me/risk-ack {"version": "<current>"}` stores the version,
  UTC time and client IP on the organisation. The current version is
  `2026-08-27`.
- Until the stored version matches, every protected route answers
  `403 risk_ack_required` with `data.required_version`. `/me`,
  `/me/risk-ack`, logout and your own password change stay reachable so a
  client can show the disclosure. When the disclosure text changes, the
  version constant is bumped and every tenant is prompted again.
- The platform operator's own organisation (organisation 1) is exempt:
  its members are staff, not clients.

**Planned:** the console screen that shows the full disclosure and posts
the acknowledgement (`docs/design/billing.md` §6). Until it ships, a
tenant acknowledges through the API call above. The disclosure text
itself is a draft under legal review (`docs/site/legal/risk-disclosure.md`);
its Summary block is reproduced at the end of every strategy page in this
guide.

## 3. First run

**Planned:** a first-run wizard. Today the first run is a short manual
checklist, all of it in the console:

1. **Settings → Scanner Suite.** Check the versioned settings document
   (`GET/POST /api/v1/screener/settings`, ADMIN, `parent_version`
   concurrency). Defaults on a fresh install:
   - `poll_interval_s` 5 (allowed 2–60; the collectors never poll faster
     than every 2 s) — hot.
   - `min_liquidity_quote` 500 — hot. Rows with less top-of-book liquidity
     than this are hidden unless a request lowers the minimum.
   - `max_plausible_spread_bps` 2000 (20 %; allowed 100–100000) — the
     asset-identity guard, see [Screener](screener.md).
   - `funding_calls_per_poll` 10 (0–50) — how many per-instrument funding
     requests a venue that needs them (OKX, Bitget, MEXC, HTX mark price)
     may issue per poll.
   - Venues: the six Tier-1 venues (Binance, OKX, Bybit, Bitget, Gate,
     MEXC) enabled; the four Tier-2 venues (KuCoin, HTX, Kraken, Coinbase)
     disabled until they have passed a 30-minute soak. Perps enabled on
     every venue except Coinbase, which has none. Enabling or disabling a
     venue is **restart-scoped**; everything else is **hot** (the badge next
     to each field says which).
   - Fees: regular-tier taker fees per venue in bps. Only some are verified
     against an official fee page; see [Venues](venues.md) before trusting a
     net number on an unverified venue.
2. **Screener.** The stats strip shows venues online, pairs tracked,
   spreads per second, data age and the poll interval. A venue that is
   enabled but not yet online shows in the count; the runbook explains the
   `restarts` and `rate_limited` counters.
3. **Alert Rules.** Create a rule (ADMIN). Rules are measurements of the
   filters you set; they carry no buy or sell wording. Leave *Automatic
   PAPER execution* off until you have set simulated balances (next
   section).
4. **Telegram** (optional). The Telegram bot token lives in the secrets
   vault and the allowlist in platform settings (`docs/deployment.md`
   §3c–§3d). Alerts are pushed only for rules with *Push to Telegram* on
   and only when the notification service is running.

Nothing in this checklist asks for an exchange API key. The screener uses
public market data only; the vault's exchange-credential group is never
read by any component and is platform-operator only.

## 4. Simulated balances

Automatic paper execution needs inventory to simulate against. Balances
are per venue and per asset in the Scanner Suite settings document
(`paper.balances`, e.g. `USDT=10000, BTC=0.5` in the *Paper balances*
column of the venue table; hot). They are:

- **Simulated.** They are not money, cannot be withdrawn and never touch
  an exchange. Every surface that shows them says "simulated balance".
- **Required for auto-paper.** A rule with auto-paper on skips with reason
  `BALANCE` when the buy venue lacks the quote asset, the sell venue lacks
  the base asset, or (perp strategies) the venue's perp wallet lacks
  collateral. Empty balances are legal — the rule then only alerts.
- **A denominator, not a return.** Results are shown first in bps of the
  amount deployed and only then in quote currency, so a large starting
  balance cannot make a result look like a return (compliance item #21).

Change balances with *Edit → Review changes → Apply*; the console shows a
diff against the active version and the apply is audited. A stale
`parent_version` (someone else applied first) is rejected and the console
offers to reload.

## 5. Where things live

| Thing | Where |
|---|---|
| Scanner Suite settings (versioned) | Settings → Scanner Suite; `screener_settings` |
| Alert rules and their events | Alert Rules; `screener_rules`, `screener_events` |
| Saved filter templates (per user) | Screener filter card; `screener_templates` |
| Paper positions, executions, balances | Auto-Paper; `paper_positions`, `paper_executions`, `paper_balances` |
| Funding history | `funding_history`, read by Funding page chart |
| Nightly paper reports | `<ARB_RECORDING_DIR>/screener-reports/<YYYY-MM-DD>/`; `screener_reports` |
| Audit trail of every mutation | Audit Log; `audit_events` |

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
