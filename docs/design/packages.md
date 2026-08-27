# Packages, entitlements and the affiliate programme

Status: PROPOSAL 2026-08-27 (T-082, T-083, T-084, T-086). Owner: product-manager.
Anchors: docs/design/crypto-arb-platform-command.md §7–8 and RULES; docs/MASTER_PLAN.md Phase 25.

Every price and limit below is a **proposal** for the operator to confirm before
the Paddle catalogue is created (paddle:catalog-setup). Nothing here is published
until docs/campaigns/ contains the paper reports the marketing site will cite.
No package, at any price, offers live execution (see §7).

## 1. Competitive scope reference (for scope comparison only)

Fetched from official pages on 2026-08-27. Used only to size the *scope* of
packages; our names, limits and prices are deliberately different (§2).

| Product | Source (accessed 2026-08-27) | Packages and list prices | Scope notes |
|---|---|---|---|
| arbitragescanner.io | https://arbitragescanner.io/plans | START $99/mo (15+ CEX, 15 scanners), BUSINESS $195/mo (30 scanners, personal manager), PLATINUM $397/mo (adds ~200 DEX via aggregator, 50 scanners), ENTERPRISE $795/mo (unlimited wallet analysis, VIP manager); long-term offers GURU $1,199/6 mo (70 scanners), VIP $2,999/yr, WHALE $9,990/15 mo (10 team licences, SLA). "Free 24-hour trial access". | Pure signal service, manual trading. Limits expressed as "scanners" (saved screener configurations), venue tiers (CEX / CEX+DEX), wallet-analysis quotas (out of our scope), support tier. Team seats only at the top offer. |
| arbitragescanner.io affiliate | https://arbitragescanner.io/affiliate | "50% of all their sales"; cookie window, payout threshold and method not stated on the page. | Very high commission, terms opaque. |
| Cryptohopper | https://www.cryptohopper.com/pricing | Explorer $24.16/mo annual ($289.92/yr), Adventurer $57.50/mo annual ($690/yr), Hero $107.50/mo annual ($1,290/yr); 3-day free trial. Limits: coins (15/50/75), positions per exchange (80/200/500), event triggers (2/5/10), strategy interval (10/5/2 min); arbitrage only on the top tier. | Bot platform, not a scanner; shows the market accepts limits on *positions*, *triggers* and *refresh interval* as tier levers. |
| Bitsgap | https://bitsgap.com/pricing | Free $0; Basic $23/mo ($276/yr); Advanced $55/mo ($660/yr); Pro $119/mo ($1,428/yr); 7-day Pro trial, no card. Limits: active bots per type (3/10/50 grid, 10/50/250 DCA), backtest depth 30/180/365 days, futures on Advanced+; 17 exchanges on all plans. | Bot platform. Confirms *history/backtest depth* and *bot count* as tier levers, and a free demo-mode tier. |

What none of them sell, and we do: automatic **paper** execution of every
signal with net-of-fee scoring and nightly evidence reports
(docs/design/scanner-suite.md §1, §4). That is the axis our tiers are built on.

## 2. Our package table (proposal)

Currency: USD list prices; Paddle localises. Annual = 10 × monthly (two
months free). Monthly and annual prices are proposals pending the operator's
sign-off and a margin check against infra cost per tenant (T-094 load tests).

| | **Watch** | **Signal** | **Operator** | **Desk** | **Institution** |
|---|---|---|---|---|---|
| Monthly | $0 | $39 | $89 | $219 | from $690 (annual only, quoted) |
| Annual | – | $390 | $890 | $2,190 | from $6,900 |
| Trial | – | 14-day Operator trial on sign-up, no card, one per organisation | ← same | ← same | pilot by agreement |
| Venues (screener + perps) | 3 fixed (Binance, OKX, Bybit) | 6 (Tier-1 set) | all Tier-1 + Tier-2 | all CEX + DEX aggregators | all, plus venue requests |
| Triangular engine venues | 1 | 2 | 4 | all supported | all |
| Concurrent alert rules | 2 | 8 | 25 | 80 | 250 (soft; raise on request) |
| Saved screener templates | 3 | 10 | 40 | unlimited | unlimited |
| Screener refresh interval | 30 s | 10 s | 5 s | 3 s | 2 s (= collector floor) |
| Alert channels | web | web + Telegram | web + Telegram + e-mail | + webhook | + webhook, multiple Telegram destinations |
| Alerts per day | 20 | 200 | 1,500 | 8,000 | 40,000 |
| Auto-paper strategies enabled | none (manual paper only) | cross-venue spot | + carry, triangular | all five (adds futures-futures, funding harvest) | all five |
| Auto-paper open positions (concurrent) | 0 | 5 | 30 | 150 | 600 |
| Paper ledgers per organisation | 1 | 1 | 3 | 10 | 25 |
| Client API access | no | no | read (60 req/min, 2 keys) | read + rule/template writes (300 req/min, 10 keys) | read + write (1,200 req/min, 50 keys), streaming WS |
| History depth (events, spreads, funding, paper ledger) | 24 h | 14 days | 90 days | 400 days | 3 years + export to object storage |
| Data export (CSV/Parquet) | no | CSV, 14 days | CSV, 90 days | CSV + Parquet, full depth | + scheduled exports |
| Campaign / evidence reports | public samples only | own rules, weekly | own rules, nightly | nightly + per-strategy comparison | nightly + custom cadence |
| Seats (members) | 1 | 1 | 3 | 12 | 40 (more on quote) |
| Roles | owner | owner | owner, admin, viewer | + operator | + custom role names, SSO (later) |
| Support | community docs | e-mail, 2 business days | e-mail, 1 business day | e-mail + shared Telegram channel, 8 business hours | named contact, 4 business hours, quarterly review |
| White-label | – | – | – | – | option (T-088, later phase) |

Rationale for the levers:

- **Venues** and **refresh interval** are our real cost drivers (collector
  polling, tick storage); they scale with price.
- **Concurrent rules** and **auto-paper positions** are the "how much can the
  suite work for me unattended" lever — the product's differentiator — so they
  grow fastest across tiers.
- **History depth** and **exports** are what a desk needs to do its own
  evaluation of our evidence, so they only open up fully at Desk.
- Watch exists so the marketing site can show real, live, delayed-refresh
  numbers instead of screenshots; it is the funnel, not a product. Watch data
  is 30 s refresh with no alerts beyond the web inbox, so it cannot be used as
  a free signal feed.

Deliberately not identical to any competitor: no "scanner" quota (we count
*rules* and *templates* separately), no wallet-analysis quotas, no
per-exchange position counts, no lifetime/multi-month bundles, no free trial
shorter than a week, and no price point shared with the three products in §1.

### Returns and evidence

No package description may cite a return, hit rate or spread size until a
paper report exists in docs/campaigns/. As of 2026-08-27 the only report
(docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3/) executed **no cycles** and makes
no profitability statement; the Scanner Suite soak (T-071) has not yet been
filed. Package copy therefore describes *capabilities and limits only* —
"no evidence yet" is the current state for every strategy.

## 3. Entitlements

Packages are rows in `packages` with a `code` and a `limits` JSON document.
The backend resolves the organisation's effective entitlements once per
request (middleware) from the active subscription and exposes them at
`GET /api/v1/me` as `entitlements`. The console only *reads* them; every
limit is enforced server-side (saas-billing skill rule). Overrides for a
single organisation (pilots, grandfathering) are stored as a partial
`entitlements_override` on the organisation and deep-merged over the package
document; the merge result is what the middleware caches (TTL 60 s,
invalidated on any subscription webhook).

### 3.1 JSON schema (draft 2020-12)

```json
{
  "$id": "https://arb-chain-bot/schemas/entitlements.v1.json",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "package_code", "venues", "rules", "alerts",
               "auto_paper", "api", "history", "seats", "support", "execution"],
  "properties": {
    "schema_version":  { "const": 1 },
    "package_code":    { "type": "string", "enum": ["watch", "signal", "operator", "desk", "institution"] },
    "venues": {
      "type": "object", "additionalProperties": false,
      "required": ["screener_max", "screener_tiers", "screener_fixed", "triangular_max", "dex_enabled", "perps_enabled"],
      "properties": {
        "screener_max":    { "type": "integer", "minimum": 0, "description": "-1 = unlimited" },
        "screener_tiers":  { "type": "array", "items": { "type": "string", "enum": ["tier1", "tier2", "dex"] } },
        "screener_fixed":  { "type": "array", "items": { "type": "string" }, "description": "non-empty = venue set is locked to these ids (Watch)" },
        "triangular_max":  { "type": "integer", "minimum": 0 },
        "dex_enabled":     { "type": "boolean" },
        "perps_enabled":   { "type": "boolean" }
      }
    },
    "rules": {
      "type": "object", "additionalProperties": false,
      "required": ["max_active", "templates_max", "min_refresh_s", "kinds"],
      "properties": {
        "max_active":    { "type": "integer", "minimum": 0 },
        "templates_max": { "type": "integer", "minimum": 0, "description": "-1 = unlimited" },
        "min_refresh_s": { "type": "integer", "minimum": 2, "description": "lowest screener refresh interval the tenant may select" },
        "kinds":         { "type": "array", "items": { "type": "string", "enum": ["spread", "carry", "basis", "funding", "triangular"] } }
      }
    },
    "alerts": {
      "type": "object", "additionalProperties": false,
      "required": ["channels", "per_day", "telegram_destinations_max", "min_cooldown_s"],
      "properties": {
        "channels":                  { "type": "array", "items": { "type": "string", "enum": ["web", "telegram", "email", "webhook"] } },
        "per_day":                   { "type": "integer", "minimum": 0 },
        "telegram_destinations_max": { "type": "integer", "minimum": 0 },
        "min_cooldown_s":            { "type": "integer", "minimum": 1 }
      }
    },
    "auto_paper": {
      "type": "object", "additionalProperties": false,
      "required": ["strategies", "max_open_positions", "ledgers_max", "max_size_quote"],
      "properties": {
        "strategies":         { "type": "array", "items": { "type": "string", "enum": ["cross_venue_spot", "carry", "futures_futures", "funding_harvest", "triangular"] } },
        "max_open_positions": { "type": "integer", "minimum": 0 },
        "ledgers_max":        { "type": "integer", "minimum": 1 },
        "max_size_quote":     { "type": "string", "pattern": "^[0-9]+(\\.[0-9]+)?$", "description": "decimal string; per-execution paper size cap in quote currency" }
      }
    },
    "api": {
      "type": "object", "additionalProperties": false,
      "required": ["enabled", "scopes", "rate_per_min", "burst", "keys_max", "streaming"],
      "properties": {
        "enabled":      { "type": "boolean" },
        "scopes":       { "type": "array", "items": { "type": "string", "enum": ["read", "rules:write", "templates:write", "paper:write"] } },
        "rate_per_min": { "type": "integer", "minimum": 0 },
        "burst":        { "type": "integer", "minimum": 0 },
        "keys_max":     { "type": "integer", "minimum": 0 },
        "streaming":    { "type": "boolean", "description": "WebSocket feed of spreads/events" }
      }
    },
    "history": {
      "type": "object", "additionalProperties": false,
      "required": ["retention_days", "export_formats", "export_scheduled", "reports"],
      "properties": {
        "retention_days":   { "type": "integer", "minimum": 1, "description": "1 = 24 h (Watch)" },
        "export_formats":   { "type": "array", "items": { "type": "string", "enum": ["csv", "parquet"] } },
        "export_scheduled": { "type": "boolean" },
        "reports":          { "type": "string", "enum": ["samples", "weekly", "nightly", "nightly_compare", "custom"] }
      }
    },
    "seats": {
      "type": "object", "additionalProperties": false,
      "required": ["max", "roles"],
      "properties": {
        "max":   { "type": "integer", "minimum": 1 },
        "roles": { "type": "array", "items": { "type": "string", "enum": ["owner", "admin", "operator", "viewer", "custom"] } }
      }
    },
    "support": {
      "type": "object", "additionalProperties": false,
      "required": ["tier", "response_hours"],
      "properties": {
        "tier":           { "type": "string", "enum": ["community", "email", "priority", "desk", "named"] },
        "response_hours": { "type": "integer", "minimum": 0, "description": "business hours; 0 = no commitment" }
      }
    },
    "execution": {
      "type": "object", "additionalProperties": false,
      "required": ["paper", "live"],
      "properties": {
        "paper": { "const": true },
        "live":  { "const": false, "description": "Hard-coded false in every package. Not an entitlement — see §7." }
      }
    },
    "white_label": { "type": "boolean", "default": false }
  }
}
```

### 3.2 Enforcement points

| Key | Enforced where | On breach |
|---|---|---|
| `venues.*` | screener settings POST, triangular venue enable, collector scheduler (tenant subscription to venue feeds) | 403 `entitlement_exceeded` with key name; existing rules referencing a disabled venue are paused, not deleted |
| `rules.max_active`, `rules.kinds`, `rules.min_refresh_s`, `rules.templates_max` | `/screener/rules`, `/screener/templates`, settings `poll_interval_s` | 403; rule evaluation loop only loads the first N enabled rules by created_at and flags the rest `paused_by_entitlement` |
| `alerts.channels`, `alerts.per_day`, `alerts.min_cooldown_s`, `alerts.telegram_destinations_max` | notification dispatcher (Redis daily counter per org), rule validation | channel silently downgraded to web inbox with a banner; daily quota → event stored, push suppressed, counted as `skipped: quota` |
| `auto_paper.*` | paperexec preconditions; ledger create | rule fires as alert only, execution `skipped: entitlement`; `max_size_quote` clamps size (decimal compare) |
| `api.*` | API-key middleware (Redis token bucket per key and per org), key creation | 429 with `Retry-After`; 403 for scope |
| `history.retention_days` | nightly retention job per org (soft-delete beyond depth), export endpoints | rows older than depth invisible via API; purged after 30 days grace on downgrade |
| `seats.max`, `seats.roles` | invitation + role change endpoints | 403; on downgrade, excess members become `suspended` (owner chooses who stays) |
| `execution.live` | `ErrLiveTradingDisabled` — code, not config | n/a: no package can flip it |

Tests required (saas-billing skill): entitlement middleware unit tests per
key; downgrade transitions (§4) leave data intact and gated, never deleted
before grace; decimal comparison for `max_size_quote`.

## 4. Upgrade, downgrade and proration rules

Implemented with Paddle subscription updates (paddle:subscription-update) and
mirrored by webhooks (paddle:subscription-sync). Paddle is the source of
truth for money; our `subscriptions` row is a mirror.

| Change | Timing | Charging | Entitlements |
|---|---|---|---|
| Trial → paid (any tier) | immediate at checkout | full period charged; trial days not credited | switch on `subscription.activated` |
| Trial expiry without card | day 15 | none | organisation drops to **Watch**; rules beyond 2 paused, positions closed on paper, data retained 30 days at trial depth then trimmed to Watch depth |
| Upgrade (monthly ↔ monthly, annual ↔ annual) | immediate | `prorationBillingMode: prorated_immediately`; period end unchanged | effective on `subscription.updated` webhook (seconds), not on the console click |
| Upgrade monthly → annual (same or higher tier) | immediate | `prorated_immediately`; new annual period starts today | as above |
| Downgrade (tier or annual → monthly) | scheduled to period end (`effectiveFrom: next_billing_period`) | no refund, no credit | current entitlements kept until period end, then §3.2 breach handling with a 30-day data grace |
| Downgrade requested inside 72 h of an upgrade | immediate, refund the unused prorated upgrade amount via Paddle credit | credit note | immediate |
| Cancel | period end (default); immediate only by support | no refund except the 14-day money-back below | Watch at period end; data grace 30 days; export offered on the cancel screen |
| Money-back | first paid period of a **new** subscription, within 14 days, once per organisation | full refund via Paddle | back to Watch immediately |
| Seat add-ons | Desk and Institution may buy extra seats at 8 % of the tier's monthly price per seat per month, prorated | Paddle line item | `seats.max` += n |
| Institution quote | annual, invoiced (Paddle invoice) | net 30 | entitlements_override document per contract |
| Past due (`subscription.past_due`) | Paddle dunning 21 days | – | full entitlements for 7 days, then read-only (alerts off, auto-paper off, API read only) until paid; Watch after dunning fails |
| Paused | not offered in v1 | – | – |

Rules that hold for every transition:

- Entitlements change only on a verified, idempotent webhook, never on a
  client-side redirect (paddle:webhooks). The console shows "pending" until
  then.
- Prices shown on the pricing page come from Paddle price previews
  (paddle:pricing-pages) — never hard-coded copies of the table above, so tax
  and localisation are correct.
- Commissions (§5) are computed on the net amount Paddle reports after tax
  and Paddle fees, and reverse on refund.

## 5. Affiliate programme (proposal)

Deliberately conservative next to the 50 % headline in §1: our margin on
low tiers cannot fund it, and an aggressive rate attracts self-referral
fraud.

| Term | Proposal |
|---|---|
| Who may join | any paying organisation (Signal+) or an approved publisher; KYC-lite (name, country, payout method) via Paddle-compatible payout data; not available to residents of sanctioned jurisdictions |
| Commission | **20 % of net subscription revenue** (after tax, refunds and Paddle fees) for the referred organisation's **first 12 months**, then 10 % for as long as both accounts stay active; Institution deals: 8 % first year only |
| Attribution | referral code or link; **last click wins**; cookie **45 days**; a signed-up organisation is locked to its referrer at the moment it creates the organisation (not at first visit) |
| Payout threshold | **$100** accrued and matured; paid monthly on the 15th for the previous calendar month |
| Maturation | commission on a period becomes payable **45 days** after the payment cleared (covers the 14-day money-back and typical chargeback windows); refunded or charged-back payments are debited from the ledger |
| Payout methods | Paddle-supported bank transfer or PayPal via the payouts report (T-084); crypto payouts not in v1 |
| Ledger | `affiliate_ledger` insert-only, decimal amounts in USD; entries: `accrued`, `matured`, `reversed`, `paid`; every entry cites the Paddle transaction id |
| Fraud rules | (1) self-referral (same person, card fingerprint, payout account or organisation domain) voids the commission and may close the affiliate account; (2) coupon-site / brand-bid / trademark-in-domain traffic voids commission; (3) referrals with a refund or chargeback rate above 20 % over 90 days are put on manual review; (4) incentivised sign-ups (cash-back to the customer) are forbidden; (5) claims about returns in affiliate material must quote a docs/campaigns/ report verbatim or say nothing — any "guaranteed", "risk-free" or invented performance figure terminates the account; (6) clawback of paid commissions for up to 180 days on proven fraud |
| Disclosure | affiliates must disclose the relationship (FTC/ASA style) and may not present themselves as us |
| Reporting | affiliate dashboard: clicks, sign-ups, trials, conversions, accrued/matured/paid — all read from the ledger, nothing estimated |

Acceptance for T-084: accrual math is decimal and tested against refunds and
partial-period proration; a self-referral fixture produces zero accrual; the
payouts report reconciles to Paddle transactions to the cent.

## 6. MASTER_PLAN task deltas

- T-082: implement §3 schema (`packages.limits`, `organisations.entitlements_override`,
  resolver middleware, `/api/v1/me.entitlements`), enforcement points in §3.2,
  tests listed there.
- T-083: implement §4 transitions; sandbox lifecycle test trial → paid →
  upgrade (prorated) → downgrade (scheduled) → past due → cancel.
- T-084: implement §5 ledger, maturation job, payouts report, fraud checks
  (1)–(3) automated, (4)–(6) as review flags.
- T-085/T-086: pricing page from Paddle previews; package copy limited to
  capabilities; Watch tier as the live demo.

## 7. Compliance note per package

Applies to **all** packages: Watch, Signal, Operator, Desk, Institution.

- The product is **market data, analytics, alerts and paper (simulated)
  execution**. No package places orders on any exchange, holds client
  funds, or takes exchange API keys. The vault's exchange group is not
  readable by any component and `execution.live` is `false` by construction
  (§3.1); it is not a purchasable entitlement, an add-on, or an Institution
  contract term.
- Live execution is not offered to any client until the production gate in
  docs/design/crypto-arb-platform-command.md is passed in full: ≥ 30 days of
  positive automatic paper evidence across regimes in docs/campaigns/, a
  security review of the execution path and key handling, the operator's
  recorded legal/regulatory decision in docs/decisions/, and a human-reviewed
  code change replacing `ErrLiveTradingDisabled`. Even then, offering
  execution to clients is a separate product and legal decision, not an
  upgrade path from these packages.
- Every package's marketing and in-product copy must: state that spreads,
  carry and paper PnL are measurements net of modelled fees and not
  predictions; carry the risk disclosure; never use "guaranteed",
  "risk-free" or "passive income"; cite only reports that exist in
  docs/campaigns/ (today: none with executed cycles — copy says "no evidence
  yet").
- Paper results are labelled "simulated" everywhere they appear, including
  exports, API responses (`"mode": "paper"`) and Telegram alerts.
- Higher tiers buy more venues, rules, positions, history and support —
  never a different regulatory posture.
- Jurisdictions: signals-only SaaS is the default product in every market;
  the compliance reviewer signs off the legal pages (terms, privacy, risk
  disclosure, refund policy) before T-085 ships, and the affiliate terms in
  §5 before T-084 opens to the public.
