# Decision: withdraw DEX coverage from the Desk and Institution packages until it is built

- Date: 2026-08-29
- Decided by (operator, legal name and role): the repository operator (product owner
  for packaging). Legal name not recorded in the material this write-up was made
  from; the operator fills it in on signing. The decision itself is evidenced by
  commit `d315558` and the 2026-08-29 status-log entry in `docs/MASTER_PLAN.md`.
- Counsel consulted (firm, person, date): none — not applicable. This decision
  *narrows* what is advertised and creates no new regulated activity. The
  template's legal fields exist for the production execution gate
  (`docs/design/crypto-arb-platform-command.md` RULES (c)), which this is not.
- Jurisdictions considered: all markets equally; the withdrawal is global and
  nothing here is jurisdiction-specific.
- Licence / exemption relied on (per jurisdiction): unchanged — signals-only SaaS,
  `docs/design/packages.md` §7.
- Scope: signals only. Unchanged: no package offers live execution, `execution.live`
  is `false` by construction, and no exchange key is read by any component.
- Evidence reviewed (docs/campaigns/ report ids, dates):
  `docs/campaigns/01M0ZPK16CXTR91MMJQ60HC2K3/` (filed 2026-08-27) — it executed **no
  cycles** and makes no profitability statement. No return, hit-rate or spread claim
  is made in this record or authorised by it; "no evidence yet" remains the state for
  every strategy.
- Security review reference: not applicable — no execution path, credential path or
  data path changed. Code touched is validation only (`internal/entitlements`,
  `internal/screener/tiers.go`).
- Insurance in place: not applicable.
- Decision: **DEX coverage is withdrawn from every package and from every published
  claim, and the platform is made unable to advertise it, until the capability is
  built.** Desk and Institution ship `ScreenerTiers: ["tier1","tier2"]` and
  `DexEnabled: false`; `entitlements.DexImplemented` is `false` and `Validate`
  refuses any document — package, stored document or tenant override — that
  advertises DEX.
- Conditions and expiry: no expiry. The withdrawal ends only when T-116 registers the
  first DEX venue and flips `DexImplemented` in the same change (§4 below). Nothing
  else lifts it, and no override may lift it for a single tenant.
- Supersedes: nothing. This is the first record in this directory. The claims it
  removes were introduced without a record, in the `packages.md` §2 proposal of
  2026-08-27.

*(The bullet block above is `TEMPLATE.md` verbatim. The sections below carry the
substance; the gate-shaped fields are answered rather than deleted so the file stays
diffable against the template.)*

## 1. What was found

The 2026-08-29 arbitragescanner parity review (`docs/design/arbitragescanner-parity.md`
§2) compared the shipped tree to what we advertise. Filed as **T-102**, P0:

- `internal/entitlements/packages.go` gave **Desk** and **Institution**
  `ScreenerTiers: ["tier1","tier2","dex"]` and `DexEnabled: true`.
- `docs/design/packages.md` §2 sold that as "all CEX + DEX aggregators" at those two
  tiers, and the claim had propagated to `billing.md` §5, `docs/site/copy/pricing.md`,
  `site/src/lib/packages.ts` and both user-guide pages.
- There was **no DEX implementation anywhere in `internal/`** — no collector, no quote
  source, no chain client, no venue constant — and **nothing read `DexEnabled`**. The
  two most expensive packages advertised a venue tier with zero code behind it, and
  the flag that was supposed to gate it was inert in both directions: it could not
  grant the capability and could not withhold it.

Nothing was mis-sold. Paddle is still in sandbox (T-083), no catalogue exists in
production, and no organisation has paid for either package. That is the only reason
this was a packaging decision and not an incident.

## 2. The two options

**(a) Withdraw the capability until it is built.** Chosen.

**(b) Hold Desk and Institution as launch blockers on T-076.** Keep the packages as
designed and do not sell either tier until DEX exists.

The trade-off, honestly:

- (b) preserves the product story. Desk stops being "Operator with more of the same"
  and keeps a capability step that a buyer can see. It also keeps a competitor-shaped
  answer available: the snapshot in `packages.md` §1 records a higher tier of theirs
  whose headline is DEX-via-aggregator coverage. **That competitor fact is
  UNVERIFIED-INDIRECT** — arbitragescanner.io is egress-blocked from this environment,
  nothing has been read from the rendered site, and it must be re-read in a browser
  before it informs any price.
- (b) costs the entire top of the revenue range. T-076 is decomposed into seven tasks
  (T-110..T-116) of which the first is still an unstarted research round, and its
  correctness core — token identity keyed on `(chain_id, contract_address)` — is the
  T-067 failure class with a lower barrier to entry. Blocking the two highest tiers on
  that means blocking them on a date nobody can name. It also leaves live, published
  copy asserting a capability that does not exist for as long as the block lasts,
  which is exactly the thing that made T-102 a P0.
- (a) costs us a weaker Desk story. With DEX gone, Desk's pitch is throughput,
  automation and team scale rather than a class of market Operator cannot see. It also
  leaves a visible hole against the competitor's line-up: a prospect comparing tier by
  tier finds no DEX row anywhere in our table, and we cannot answer it until T-116.
- (a) buys the thing the platform is actually built on: every published capability
  resolves to something in the tree. That rule is worth more than one row of a pricing
  table, and it is cheap to keep now and expensive to reinstate later.

## 3. The decision, and why it is enforced rather than applied

Option (a), on 2026-08-29. The correction is not a documentation edit that a later
edit can undo:

- `internal/entitlements/capabilities.go` holds `const DexImplemented = false` and the
  `ErrUnimplemented` sentinel — deliberately distinct from a plain schema failure,
  because a rejected *word* is a typo and a rejected *capability* is the platform
  refusing to sell something that does not exist.
- `Validate` calls `checkImplemented` after the enum checks, so **any** document
  advertising `dex_enabled: true` or the `"dex"` screener tier is refused: the shipped
  packages, a stored document, and a per-tenant `entitlements_override`. The resolver
  degrades a widening override to the bare package rather than serving it. A pilot
  contract cannot buy the tier back.
- `internal/screener/tiers.go` makes the Tier-1/2/3 grouping machine-readable for the
  first time (it had lived only in comments and plan prose), which is what lets
  `TestAdvertisedTiersResolveToRegisteredVenues` assert the acceptance criterion
  directly: every advertised screener tier resolves to at least one registered venue.
  The test lives in an external test package so the policy layer keeps no screener
  import.
- Verified by mutation: re-introducing the exact defect fails four tests, including
  the pre-existing `TestPackagesValid`.

The generalised rule is now recorded in the command document ("Parity review", item
3): **an advertised package capability must resolve to something in the tree.** A
boolean that nothing reads is a promise nothing keeps.

## 4. What reverses it

One constant, in one change, under one condition.

T-116 flips `DexImplemented` to `true` **in the same change that registers the first
DEX venue**. `TestDexCapabilityMatchesTree` enforces both directions: flipped without a
registered venue fails, and a registered venue without the flip fails as under-selling.
The `"dex"` tier deliberately stays in `enumTiers` and `schema.v1.json`, so the
reversal needs no schema bump and no override migration.

Before Desk may advertise DEX again, all of the following must be true:

1. T-110 has filed `docs/research/dex-endpoints.md` with each aggregator's quote
   endpoint, size behaviour, gas estimate, rate limit and key policy — URL and access
   date, VERIFIED or UNVERIFIED per line. Everything in `dex-arbitrage.md` §3 is
   UNVERIFIED until then and no collector is written before it.
2. T-111..T-115 have shipped a `QuoteSource`, canonical token lists with
   `(chain_id, contract_address)` identity (including the symbol-collision fixture that
   proves a fake `USDC` is rejected), a gas-aware cost model, and at least one lane.
3. At least one DEX venue is registered in `screener.VenueTiers` under `TierDex`, so
   `VenuesInTier(TierDex)` is non-empty.
4. The copy withdrawn here is restored in the same change — `packages.md` §2,
   `billing.md`, `docs/site/copy/pricing.md`, `site/src/lib/packages.ts` and both
   user-guide pages — and says only what the lane does.
5. The standing MEV caveat rides with it: DEX paper evidence never satisfies the
   production execution gate on its own, because the paper model omits the dominant
   adversarial cost on a real swap.

Restoring the tier without (3) is not possible; restoring it without (1), (2), (4) or
(5) would be a new decision and needs a new record superseding this one.

## 5. The consequence this exposed: Desk and Operator now have the same venue coverage

### 5.1 Verified state of the tree

Checked directly, not inferred:

- Desk and Operator both carry `ScreenerMax: Unlimited`, `ScreenerFixed: []` and
  `ScreenerTiers: ["tier1","tier2"]`. The screener venue row is now **identical**.
- **`ScreenerTiers` is enforced by nothing.** `CheckVenues` in
  `internal/entitlements/limits.go` checks `screener_fixed` and `screener_max` only;
  no `Check*` function reads `screener_tiers`, and its two call sites
  (`internal/api/screenerapi.go`, on settings apply and on rule validation) pass venue
  ids with no tier map. The tier row has therefore never gated anything at any tier —
  a Signal tenant (`["tier1"]`, max 6) can enable six Tier-2 venues today.
- The tier vocabulary is also **stale**: `enumTiers` is `["tier1","tier2","dex"]`,
  while the tree ships fifteen venues of which five are Tier-3 (Crypto.com, Bitfinex,
  BingX, WhiteBIT, BitMart — T-078, soaked 2026-08-28, all five passed, enabled by
  default). Five shipped venues belong to no advertised tier at all, and
  `Defaults()` enables every one of them for everybody.
- The one venue-shaped difference left between the two documents is
  `triangular_max`: 4 for Operator, unlimited for Desk. `platform.CompiledVenues` is
  `{binance: true}` — one connector, with OKX blocked (T-050) and the rest behind
  T-051. It is a limit of 4 versus unlimited over a set of size one.

So: the venue row does not differentiate Desk from Operator, and never enforced the
difference it printed.

### 5.2 What Desk still buys over Operator

Rules 25 → 80. Templates 40 → unlimited. Refresh floor 5 s → 3 s. Alerts/day
1,500 → 8,000, cooldown 30 s → 10 s, plus the webhook channel. Auto-paper strategies
3 → all five (adds futures-futures and funding harvest), open positions 30 → 150,
ledgers 3 → 10, per-execution paper cap 25,000 → 100,000. API read → read +
`rules:write` + `templates:write`, 60 → 300 req/min, 2 → 10 keys. History 90 → 400
days, CSV → CSV + Parquet, nightly → nightly-with-comparison. Seats 3 → 12, plus the
operator role and paid seat add-ons.

Two of those levers are weaker than the table implies, and should stop being leaned
on: the refresh step is real only for fast venues (Crypto.com self-paced to roughly
15 s in the 2026-08-28 soak regardless of the configured interval), and the support
row reads as a step but is not one — Operator and Desk both resolve to 8 business
hours in the shipped documents (`billing.md` §2). Desk's genuine support difference is
the shared Telegram channel, not a faster number.

### 5.3 Recommendation

**Keep $219 and $89 as shipped, and restore the venue row with Tier-3 rather than with
a price cut.** Concretely:

1. **Do not move the prices.** $219 was never priced on DEX: the `packages.md` §2
   rationale attributes the step to venues, refresh interval, concurrent rules,
   auto-paper positions, history depth and exports, and DEX appears nowhere in it. Nothing was sold, so no buyer perceives a
   loss to compensate for. Cutting Desk now and raising it again at T-116 is a worse
   story to a customer than holding a price that never moved. The $89 → $219 step is a
   **team step**, and it survives the DEX withdrawal on that basis alone: 3 seats to 12
   is $29.67 per seat down to $18.25 per seat, and a four-person desk simply cannot be
   accommodated on Operator at any price. Add 3.2× the rules, the write API, and 400
   days with Parquet — which is what a desk needs to audit our evidence itself — and
   2.46× is defensible without a venue row.
2. **Restore the venue row honestly, from venues that already exist.** Give Desk and
   Institution `["tier1","tier2","tier3"]` and leave Operator at `["tier1","tier2"]`.
   This takes nothing away from anyone: Operator's *advertised* coverage stays exactly
   what it was sold as (Tier-1 + Tier-2, ten venues), and what disappears is only the
   unenforced accident by which every tier could reach all fifteen. Desk gains the five
   Tier-3 venues it already has copy for ("all CEX venues", `billing.md` §3), and every
   one of them is coded, conformance-tested and soaked. This is the T-102 rule applied
   in the positive direction: the row differentiates because the code differs.
3. **Enforce it, or it is decorative again.** `screener_tiers` needs a real check
   (T-104 below). Without it, item 2 changes a marketing table and nothing else — and
   the record would be repeating the mistake it exists to document.
4. **Do it before Paddle leaves sandbox.** Tightening an unenforced limit is free
   today and is a downgrade with a 30-day data grace and a paused-not-deleted migration
   (`packages.md` §3.2, §4) after the first paying tenant. This is the last cheap
   moment.

Not recommended: repricing Desk to around $169 to reflect the missing tier. It concedes
a loss no customer experienced, and it makes T-116 a price rise.

Evidence note, because this section is about what a tier is worth: whether Tier-3
venues surface more or larger cross-venue spreads than Tier-1/2 is **unmeasured — no
evidence yet.** The recommendation rests on venue count and shipped code, not on
expected returns, and no copy derived from it may say otherwise.

## 6. MASTER_PLAN deltas proposed by this record

Drafted here, not yet inserted into `docs/MASTER_PLAN.md`.

### T-104 Screener tier entitlement is advertised but enforced by nothing
- priority: P1 · component: entitlements · status: TODO
- description: add `"tier3"` to `enumTiers` and to `schema.v1.json`'s
  `venues.screener_tiers` enum (additive; `schema_version` stays 1, no override
  migration). Desk and Institution become `["tier1","tier2","tier3"]`; Watch, Signal
  and Operator are unchanged. Add a tier check to the venue gate — the tier→venue map
  must be **injected** (`CheckVenueTiers(venues []string, tierOf func(string) string)`
  or equivalent), never imported, so `internal/entitlements` keeps the no-screener-
  import seam T-102 established; the API layer already imports both and resolves it
  from `screener.VenueTiers`.
- business reason: the venue row is the differentiator this decision removed from Desk
  and the only one available from already-shipped code; unenforced, it is copy.
- acceptance: an Operator principal enabling a Tier-3 venue gets 403
  `entitlement_exceeded` with key `venues.screener_tiers`, on both settings apply and
  rule create; `TestAdvertisedTiersResolveToRegisteredVenues` still passes and still
  fails for a tier with no registered venue; a settings document that already enables
  Tier-3 venues under a sub-Desk package leaves those venues **paused, not deleted**
  (`packages.md` §3.2); `Defaults()` enabling all fifteen venues does not make a
  sub-Desk tenant's first save impossible.
- dependencies: none · risk: must land before T-083 leaves sandbox, or it becomes a
  downgrade transition with a grace window instead of a correction.

### T-105 Desk positioning: team tier, not venue tier
- priority: P2 · component: product/docs · status: TODO
- description: docs only, **no price change**. `packages.md` §2 and `billing.md` §3:
  Desk's venue cell becomes the fifteen-venue set once T-104 lands and Operator's reads
  Tier-1 + Tier-2 (ten venues); the support row stops implying a faster response than
  Operator receives (both are 8 business hours) and differentiates on the shared
  Telegram channel; the triangular row states the honest ceiling — one connector
  shipped, T-050/T-051 pending — instead of "4 vs all supported"; the Desk rationale
  leads on seats, write API and history depth.
- acceptance: every row of the §2 table differs between adjacent tiers in at least one
  value that exists in `internal/entitlements/packages.go`, or the row is annotated as
  non-differentiating; `billing.md` §3 Paddle descriptions match the shipped documents
  on venue coverage word for word; copy lint (T-085) passes — no figure without a
  `docs/campaigns/` citation.
- dependencies: T-104.
