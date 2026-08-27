# Tenancy, entitlements and Paddle billing (T-081 / T-082 / T-083)

Status: IMPLEMENTED 2026-08-27 on branch scanner-suite (backend). Owner: billing engineer.
Anchors: docs/design/packages.md (package table §2, schema §3, proration §4,
affiliate §5, compliance §7); docs/compliance/review-2026-08-27.md items 1, 3,
9, 10, 11, 23; saas-billing skill; paddle plugin skills (catalog-setup,
checkout-web, webhooks, subscription-sync, subscription-update,
subscription-cancel, customer-portal, sandbox-testing).

Nothing in this document changes the production gate: `execution.live` is
`false` in every package, every override and every resolved document by
construction (§2.3), the exchange-credential vault group is platform-operator
only (§1.3), and live trading stays behind `ErrLiveTradingDisabled`.

## 1. What was built

### 1.1 Tenancy (migration `000013_tenancy`)

| Table / column | Purpose |
|---|---|
| `organisations` | `id`, `name`, `package_code` (watch/signal/operator/desk/institution), `country`, `customer_type` (consumer/business — compliance #11), `risk_ack_version/at/ip` (compliance #3), `trial_ends_at`, `entitlements_override` JSONB, `created_at`, `referred_by` (000014) |
| `memberships` | `(org_id, user_id)` → `role` OWNER/ADMIN/MEMBER/VIEWER, `status` active/suspended |
| `users.platform_admin` | operator staff flag; backfilled `TRUE` for existing ADMIN accounts; never set by a package, membership or webhook |
| `screener_rules/templates/events/paper_positions/paper_executions/paper_balances.org_id` | `NOT NULL DEFAULT 1`, FK to organisations; balances PK becomes `(org_id, venue, asset)` |

Organisation 1 = the platform operator (`package_code = institution`). Every
pre-existing user became a member of it (ADMIN→OWNER, OPERATOR→ADMIN,
VIEWER→VIEWER) and every pre-existing screener row was backfilled to it.

Code: `internal/tenancy` (types, context scope, memory store),
`internal/storage/tenancy.go` (pgx store + `entitlements.Source`). Scoping
works through the request context: the API middleware calls
`tenancy.WithOrg(ctx, orgID)`; every screener store query reads it back
(`orgFilter` in `internal/storage/screener.go`). A context without a scope
(engine loop, background jobs) sees every organisation and writes to the
platform organisation; engine-side event/position/execution inserts inherit
their rule's `org_id` via a sub-select, so tenant paper ledgers stay separate
without the engine knowing about tenants.

### 1.2 Entitlements (`internal/entitlements`)

- `schema.v1.json` is packages.md §3.1 verbatim (embedded); `validate.go`
  mirrors it in Go and a test pins both (enums, required keys, `live` const).
- `packages.go` holds the five package documents from packages.md §2.
- `Resolve(Input)` = package document ⟶ deep-merge `entitlements_override`
  ⟶ re-validate ⟶ apply subscription state (trial expiry → Watch;
  canceled/paused → Watch; past_due > 7 days → read-only: alerts 0,
  auto-paper off, API read-only). The merged document is walked
  case-sensitively against the schema before decoding so
  `{"Execution":{"Live":true}}` cannot slip past `encoding/json`'s
  case-insensitive matching.
- `Resolver` caches per organisation for 60 s and is invalidated by every
  subscription webhook and override change. A rejected override degrades to
  the bare package and is logged — it never widens service.
- Enforcement points (packages.md §3.2) and where they live:

| Key | Enforced in | Response |
|---|---|---|
| `rules.max_active`, `rules.kinds`, `rules.min_refresh_s`, `rules.templates_max` | `POST/PUT /screener/rules`, `POST /screener/templates`, `POST /screener/settings` | 403 `entitlement_exceeded` + `data.key/limit` |
| `venues.screener_fixed`, `venues.screener_max` | rule venues and enabled venues in settings | 403 |
| `alerts.channels` (`telegram`), `alerts.min_cooldown_s` | rule validation | 403 |
| `alerts.per_day` | `entitlements.DailyCounter` (per org, UTC day) — dispatcher wiring lands with the Redis counter | counter answers false ⟶ `skipped: quota` |
| `auto_paper.strategies`, `auto_paper.max_size_quote` (decimal compare), `auto_paper.max_open_positions` | rule validation **and** `paperexec.Options.Entitle` precondition (skip reason `entitlement`) | 403 / position row `SKIPPED` |
| `api.enabled/scopes/rate_per_min/burst` | `entitlements.RateLimiter` + `CheckAPIScope` — API keys ship with T-08x; middleware hook ready | 429 + `Retry-After` / 403 |
| `history.retention_days` | `GET /screener/events` hides rows older than the depth (purge job separate, 30-day grace) | rows invisible |
| `seats.max`, `seats.roles` | `POST /org/members`, `POST /org/members/{id}/role` | 403 |
| `execution.live` | code (`ErrLiveTradingDisabled`); validator rejects any document with `live=true` | n/a |

`GET /api/v1/me` (alias `GET /api/v1/auth/me`) returns `user_id`, `role`,
`platform_admin`, `org`, `org_role`, `risk_ack_required`, `risk_ack_version`
and `entitlements` (with `entitlements.status`: subscription state, read-only
flag, effective package, trial end).

### 1.3 Platform admin vs tenant (compliance #1)

`Principal.PlatformAdmin` comes from `users.platform_admin` only.
`requirePlatformAdmin` guards: user management (`/users*`), platform settings
apply/rollback, engine restart, organisation creation and overrides, price
mapping. The secrets API filters the `exchange` group out of `GET /secrets`
and answers 403 `platform_admin_required` on `PUT/DELETE
/secrets/<venue>_api_*` for anyone else — including tenant OWNER/ADMIN who hold
the console ADMIN role (test: `TestVaultExchangeGroupIsPlatformAdminOnly`).

### 1.4 Risk acknowledgement (compliance #3, #11)

`POST /api/v1/me/risk-ack {version}` stores version, UTC time and client IP on
the organisation. Until the organisation's stored version equals
`api.Server.RiskAckVersion` (`app.RiskDisclosureVersion = "2026-08-27"`), every
protected route answers 403 `risk_ack_required` with `data.required_version`;
`/me`, `/me/risk-ack`, logout and own-password stay reachable so the console
can show the disclosure. The platform organisation is exempt (its members are
staff, not clients). Bumping the constant re-prompts every tenant.

### 1.5 Paddle (migration `000014_billing`, `internal/billing/paddle`)

| Table | Purpose |
|---|---|
| `billing_prices` | `price_id → package_code, billing_interval` (operator-maintained, §3) |
| `subscriptions` | mirror: `org_id` PK, `paddle_customer_id`, `paddle_subscription_id`, `price_id`, `status`, `current_period_end`, `cancel_at_period_end`, `past_due_since`, `scheduled_price_id`, `updated_at` |
| `paddle_events` | idempotency: `event_id` PK, `type`, `occurred_at`, `payload`, `received_at`, `processed_at` |
| `affiliate_accounts`, `affiliate_ledger` | T-084 ledger schema (insert-only, NUMERIC, cites `transaction_id`) |

Routes (`internal/api/billingapi.go`):

| Route | Auth | Behaviour |
|---|---|---|
| `POST /api/v1/billing/webhook` | Paddle-Signature only | verify HMAC (`ts=…;h1=…` over `ts:body`, 5-minute tolerance, rotation-safe), 401 on failure; dedupe on `event_id`; apply; 500 on internal error so Paddle retries; replay ⟶ 200 `{"replayed":true}` with no side effects |
| `GET /api/v1/billing/subscription` | member | mirror row + `entitlements.status` |
| `POST /api/v1/billing/checkout {price_id}` | OWNER/ADMIN + CSRF | no live subscription ⟶ `POST /transactions` with `custom_data.org_id`, returns `transaction_id` + public `client_token`/`environment` for `Paddle.Checkout.open({transactionId})`; live subscription ⟶ `PATCH /subscriptions/{id}` with the §4 proration mode, returns `pending:true` |
| `GET /api/v1/billing/portal` | OWNER/ADMIN | `POST /customers/{ctm}/portal-sessions`, returns only `urls.general.overview` |
| `POST /api/v1/billing/cancel` | OWNER/ADMIN + CSRF | `POST /subscriptions/{id}/cancel {effective_from: next_billing_period}` |
| `GET /api/v1/billing/prices`, `PUT /api/v1/billing/prices/{price_id}` | member / platform admin | price map |

Events handled: `subscription.created/updated/activated/trialing/past_due/paused/resumed/canceled`
and `transaction.completed`. The state machine (`lifecycle.go`, pure,
convergent) implements packages.md §4:

- **Trial → paid**: `subscription.activated/created` with a mapped price sets
  the package, clears the trial clock.
- **Upgrade**: `prorated_immediately`; package switches on the webhook.
- **Downgrade**: Paddle has no "switch the item later" primitive, so the item
  is replaced now with `full_next_billing_period` (no refund, no credit, full
  new price at the next renewal) and *our* row records `scheduled_price_id`
  while `package_code` stays at the current tier until `current_period_end`;
  the renewal webhook (new billing period) or `Service.RunScheduled` makes it
  effective. Every update uses `on_payment_failure: prevent_change`.
- **Past due**: `past_due_since` is set once and preserved across retries;
  the resolver keeps full entitlements 7 days, then read-only.
- **Cancel**: `scheduled_change.action=cancel` ⟶ `cancel_at_period_end`,
  entitlements kept; `subscription.canceled` ⟶ package Watch.
- Unmapped price: mirrored and logged, entitlements untouched (never guessed).
- Older events never roll back newer state (`occurred_at` compare).

No card data reaches this process: checkout runs in Paddle's overlay, payment
methods live in Paddle's portal, and our API only ever sees ids and statuses.

## 2. Package documents — values derived from packages.md

Everything in `internal/entitlements/packages.go` comes from the packages.md
§2 table. Cells the table leaves implicit were filled conservatively and are
listed here so the product manager can adjust them in one place:

| Key | Watch | Signal | Operator | Desk | Institution | Note |
|---|---|---|---|---|---|---|
| `rules.kinds` | spread, basis | +carry | +funding, triangular | same | same | Watch is the funnel; funding/triangular kinds arrive with their collectors |
| `alerts.min_cooldown_s` | 300 | 60 | 30 | 10 | 5 | tracks refresh interval |
| `alerts.telegram_destinations_max` | 0 | 1 | 1 | 1 | 5 | "multiple Telegram destinations" is Institution-only in the table |
| `auto_paper.max_size_quote` | 0 | 5000 | 25000 | 100000 | 1000000 | decimal strings; per-execution paper size cap |
| `api.burst` | 0 | 0 | 20 | 60 | 200 | one third of the per-minute rate |
| `support.response_hours` | 0 | 16 | 8 | 8 | 4 | business hours (2 days = 16 h) |
| `venues.perps_enabled` | true | true | true | true | true | the table's venue row covers "screener + perps" |
| `seats.roles` | owner | owner | owner, admin, viewer | +operator | +custom | MEMBER membership maps to schema role "operator" |

## 3. Paddle catalogue the operator must create (paddle:catalog-setup)

Sandbox first, then live. Names below are ours (packages.md §2); prices are
the **proposals** in packages.md and stay proposals until the operator signs
them off. Tax category: `standard` (SaaS). Currency: USD; let Paddle localise.

| Product (name / description) | Price name | Interval | Unit price (proposal) | Trial |
|---|---|---|---|---|
| **Signal** — screener alerts on the Tier-1 venue set with automatic paper execution of cross-venue spot signals (simulated; no live orders) | Signal monthly | month | 39.00 | none (trial is our own 14-day Operator trial, no card) |
|  | Signal annual | year | 390.00 | none |
| **Operator** — all Tier-1 + Tier-2 venues, 25 rules, carry + triangular paper strategies, e-mail alerts, read API | Operator monthly | month | 89.00 | none |
|  | Operator annual | year | 890.00 | none |
| **Desk** — all CEX + DEX aggregators, 80 rules, all five paper strategies, webhook alerts, read/write API, 12 seats | Desk monthly | month | 219.00 | none |
|  | Desk annual | year | 2190.00 | none |
| **Institution** — quoted; annual invoice via Paddle | Institution annual (per quote) | year | from 6900.00 (custom) | pilot by agreement |
| **Seat add-on** (Desk, Institution) | Extra seat | month | 8 % of tier monthly price (17.52 / quoted) | — |

Watch has no Paddle product (free tier; `organisations.package_code =
watch`). Every product description must describe capabilities and limits only
— no returns, hit rates or spreads (packages.md "Returns and evidence";
compliance #4/#5): the only report in docs/campaigns/ executed no cycles.

After creating each price, map it:

```
PUT /api/v1/billing/prices/pri_01h...   {"package_code":"signal","billing_interval":"month"}
```

(platform admin + CSRF). Unmapped prices are mirrored but never change a
package.

Notification destination (paddle:webhooks): URL `https://<console
host>/api/v1/billing/webhook`, events `subscription.*` and
`transaction.completed`; copy the destination's secret into the vault as
`paddle_webhook_secret`.

## 4. Environment and secrets

| Name | Where | Applies | Purpose |
|---|---|---|---|
| `paddle_api_key` (vault) / `PADDLE_API_KEY` (env fallback) | secrets registry, provider group | immediately | server-side API key (transactions, subscription updates, portal sessions) |
| `paddle_webhook_secret` / `PADDLE_WEBHOOK_SECRET` | secrets registry, provider group | immediately | notification-destination secret for signature verification |
| `PADDLE_CLIENT_TOKEN` | env (config) | boot | public Paddle.js client-side token, returned by `/billing/checkout` and `/billing/prices` |
| `PADDLE_ENV` | env (config) | boot | `sandbox` (default) or `production`; selects `sandbox-api.paddle.com` vs `api.paddle.com` |

`TestRegistryIsClosed` now pins four provider entries. The exchange group is
unchanged: no env fallback, never consumed, platform-admin only.

## 5. Sandbox test procedure (paddle:sandbox-testing)

1. Sandbox account → create the catalogue in §3 → create a client-side token
   (`PADDLE_CLIENT_TOKEN`) → create the notification destination pointing at
   a tunnelled `/api/v1/billing/webhook` → store both secrets in the vault
   (`PUT /api/v1/secrets/paddle_api_key`, `PUT /api/v1/secrets/paddle_webhook_secret`).
2. Map every sandbox price with `PUT /api/v1/billing/prices/{price_id}`.
3. Create a tenant organisation (`POST /api/v1/orgs`, platform admin): it
   starts on the 14-day Operator trial. Log in as its owner; `GET /api/v1/me`
   shows `risk_ack_required: true`; acknowledge with `POST /api/v1/me/risk-ack`.
4. **Trial → paid**: `POST /api/v1/billing/checkout {"price_id": <Signal monthly>}`,
   open the overlay with the returned `transaction_id`, pay with test card
   `4242 4242 4242 4242`. Expect `transaction.completed` then
   `subscription.created/activated`; `GET /api/v1/me` shows
   `entitlements.package_code = signal`, `status.subscription = active`.
5. **Upgrade**: checkout with the Desk monthly price; response `pending:true,
   proration: prorated_immediately`; the `subscription.updated` webhook flips the
   package to desk within seconds; `current_period_end` unchanged.
6. **Downgrade**: checkout with the Operator monthly price; response
   `proration: full_next_billing_period`; `GET /billing/subscription` shows
   `scheduled_price_id` set and `package_code` still desk. Use the sandbox
   simulator (or wait for the renewal) to send `subscription.updated` with a
   new `current_billing_period` → package becomes operator.
7. **Past due**: switch the customer's card to a declining test card in the
   portal (`GET /api/v1/billing/portal`) and simulate `subscription.past_due`;
   `past_due_since` is set; after 7 days (`entitlements` test covers the clock)
   the document is read-only.
8. **Cancel**: `POST /api/v1/billing/cancel` → `subscription.updated` with
   `scheduled_change.action = cancel` → `cancel_at_period_end: true`, package
   unchanged; simulate `subscription.canceled` → package watch.
9. **Idempotency**: re-send any event from the Paddle dashboard; the endpoint
   answers 200 `{"replayed":true}` and nothing changes.
10. **Signature**: post the same body with a wrong secret → 401; Paddle marks
    it failed and retries.

Automated equivalents: `internal/billing/paddle` (signature, replay,
lifecycle, checkout/portal against a fake Paddle API), `internal/storage`
(`TestBillingStoreIdempotency` over pgx), `internal/api` (RBAC, org scoping,
risk ack, enforcement), `internal/entitlements` (schema, override, live-false).

## 6. Open items

- Alert-per-day counter and API-key rate limiter are implemented and tested
  in `internal/entitlements` but not yet wired into the dispatcher / an API-key
  middleware (API keys do not exist yet).
- Retention purge job (30-day grace) and seat suspension on downgrade are
  policy in packages.md; the read paths already hide/gate, the jobs are not
  written.
- Self-service sign-up (T-085) will call `Tenancy.CreateOrg` with the trial
  and present the risk disclosure before the first request; today the
  platform operator creates tenant organisations.
- T-084 affiliate: `internal/billing/affiliate` implements decimal accrual
  (20 % / 10 % / Institution 8 %), 45-day maturation, refund reversal,
  self-referral = zero, and the balance/payable computation; it is wired to
  `transaction.completed` via `paddle.Service.OnTransaction` and persisted in
  `affiliate_ledger`. Not yet: the maturation/payout job, the payouts report,
  enrolment/KYC-lite, and fraud rules (2)–(3) automation.
- Console: read `entitlements` and `risk_ack_required` from `/api/v1/me`, show
  "pending" after checkout until the webhook lands, render `status.read_only`.
