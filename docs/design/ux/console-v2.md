# Console v2 — UX specification (Scanner Suite shell, client + operator)

Status: DRAFT 2026-08-27. Author: UX design (design-only; no application code).
Scope: `web/src/components/ConsoleShell.tsx` v2, and the eight pages this
brief asks for: Screener, Perpetuals, Funding, Calculator, Alert Rules
(`/scanner-alerts`), Auto-Paper, Evidence, plus onboarding and the
notification centre that sit around them.

**Supersedes**: `docs/design/console-ux-audit.md` §2 ("keep the flat sidebar
… no need for collapsible mega-groups") for the *shell* only — that
recommendation predates the Scanner Suite and the multi-tenant client
console; this document is the current word on navigation shape.
`console-ux-audit.md` §4 (status vocabulary, empty/error states,
confirmation rules, table density, keyboard/a11y, responsive rules) **stays
in force** and is extended, not replaced, below. Where this document is
silent, `console-ux-audit.md` and `client-area.md` govern.

**Task tags** — nothing here is one shippable batch:

| Pages | Task |
|---|---|
| Screener, Perpetuals, Funding, Calculator, Alert Rules, Auto-Paper | T-069 (Console pages, Scanner Suite group) — depends on T-066/T-067/T-068/T-070/T-071 for live data |
| Evidence | T-080 (Phase 24) — depends on auto-paper campaign data existing across strategies |
| Shell v2 (icon rail, collapsible groups, package gating, notification centre) | new, unassigned — propose **T-069b** so it isn't silently folded into the page build |
| Client console (tenant re-skin, package gating enforcement) | Phase 25 (T-081..T-088) |
| Onboarding wizard | Phase 25, wired to T-068's screener settings + T-070's rules |
| Light theme | scanner-suite.md §5 already commits to it; token set below feeds `console-ux-audit.md` BL-24 |

Every page below inherits, unchanged: decimal money math end-to-end
(numbers are strings from the backend, the client only formats), RBAC
enforced by the backend (`can()` only hides, never gates), CSRF +
`parent_version` on every versioned write, audit on every mutation, and the
platform-wide rule that **nothing is ever described as guaranteed**.

---

## 1. Console identity: client vs operator

Two consoles, one component library, one shell shape. They differ in **who
they're for** and **what nav groups render**, not in visual language.

| | Operator console (existing `web/`, unchanged host) | Client console (Phase 25 re-skin, tenant-scoped) |
|---|---|---|
| Audience | us — the platform operator/team | a subscribing organisation's users |
| Data scope | the whole deployment | one tenant's rules, paper sessions, alerts, reports |
| Nav groups | Operate, Scanner Suite, Portfolio, Research, Control, System, Settings (all seven, §2.2) | Operate *(tenant-scoped subset — Overview + Paper Trading only, §2.2)*, Scanner Suite, Portfolio *(Portfolio & Balances + PnL & Analytics only — no Orders/Fills, which are triangular-engine internals)*, Alerts, Reports, Settings — no Research, no System, no cross-tenant anything |
| Exchange credentials | none in either console today; the vault's `exchange` group exists for a **future, separately reviewed, read-only** consumer (settings-expansion.md §3.3) | **absolute non-goal.** No key field, masked or not, anywhere in the client console. A signals-only SaaS product with a client-facing exchange-credential surface implies execution; it does not have one. If a client asks "how do I connect my exchange," the answer lives in marketing copy ("we never touch your funds"), not a settings page. |
| Users & roles | full org/user/RBAC admin across the deployment | tenant-scoped invite/role management, gated by the tenant's own package (Desk/Enterprise get more seats) |
| Evidence page | full view: all four production-execution-gate criteria (a)-(d), the operator's own legal decision doc, links into `docs/campaigns/` and `docs/decisions/` | performance-only view: net PnL after fees / hit rate / drawdown / sample size per strategy, scoped to the tenant's own rules and paper sessions — **no gate-criteria checklist**, since (b) security review and (c) legal decision are the operator's internal record, not a client-facing status. Both views share the same underlying numbers component (§6.7); they differ in which panels render. |
| Billing / packages | none — we don't buy our own packages | Paddle-driven: current package, usage against its limits, upgrade CTA |

The client console is the operator console's component library and page
patterns, re-rendered against tenant-scoped data with a narrower nav and
package gating layered on top — not a separate design system. A component
built for one page ships for both.

---

## 2. Shell v2

### 2.1 Icon rail with collapsible product groups

Replace the always-expanded text sidebar with a two-density rail:

```
┌────┬──────────────────────────────┐
│ [≡]│ ARB CONSOLE          [🔔 3]   │  ← top bar: brand, notification bell
├────┼──────────────────────────────┤
│ ▣  │ [● PAPER] live trading off    │  ← mode banner, unchanged position/logic
│ ▤  ├──────────────────────────────┤
│ ⚡ │ OPERATE                    ⌄  │  ← group header, collapse chevron
│ 🔍 │   Overview                    │
│ 📈 │   Scanner                     │
│ 🧮 │   Triangles                   │
│ ⚠  │   Opportunities                │
│ ⚙  │   Paper Trading                │
│    │ SCANNER SUITE               ⌄ │
│    │   Screener                    │
│    │   Perpetuals                  │
│    │   Funding                     │
│    │   Calculator                  │
│    │   Alert Rules                 │
│    │   Auto-Paper           🔒Pro  │  ← package-gated, §2.4
│    │   Evidence                    │
│    │ PORTFOLIO                   ⌄ │
│    │   ...                         │
└────┴──────────────────────────────┘
```

- **Icon rail** (leftmost, ~44px, always visible ≥ `md`): one icon per
  *group*, not per page — clicking an icon scrolls/expands that group in
  the label column and collapses others (accordion, one group open at a
  time by default; an operator can pin more than one open — state persists
  per-user in `localStorage`, not the backend, since it's presentation
  only). Icons are **our own glyphs** (simple geometric marks consistent
  with the existing `Badge`/dot vocabulary), never a copied icon set.
- **Label column**: exactly today's `NavContent`, grouped, with a chevron
  per group header that rotates on expand/collapse. Every icon has a
  visible text label next to it when its group is expanded — the rail
  is never icon-only for a sighted user with the panel open; it only
  becomes icon-only when the operator explicitly collapses the whole
  sidebar to icon-rail width (a new "collapse sidebar" toggle at the
  bottom, persisted per-user) for screen-space-constrained setups.
- **Settings** stays pinned at the bottom outside the scrollable group
  list, unchanged from today.
- **Mode banner** stays exactly where it is today, full-width under the
  brand row, unaffected by rail collapse — it is chrome, not nav.
- The existing disabled-entry pattern (`ConsoleShell.tsx:350-358`) is kept
  for "not built yet" items; §2.4 below adds the two more grey states this
  spec needs on top of it.

### 2.2 Nav groups (operator console, full)

| Group | Icon (our own glyph) | Pages |
|---|---|---|
| Operate | ▣ filled square | Overview, Scanner, Triangles, Opportunities, Paper Trading |
| Scanner Suite | ⚡ bolt-in-circle | Screener, Perpetuals, Funding, Calculator, Alert Rules, Auto-Paper, Evidence |
| Portfolio | ◨ split square | Portfolio & Balances, PnL & Analytics, Orders, Fills |
| Research | ⌬ node graph | Campaigns, Replay & Backtesting, AI Advisor |
| Control | ◆ diamond | Strategies, Risk Center, Alerts, Reports |
| System | ⬡ hex | Exchanges, Markets, System Health, Audit Log, Telegram |
| — (pinned) | ⚙ gear | Settings, Users & Security |

**Client console groups are shorter, not just fewer** — the table above is
a group *count* summary; the actual per-group item lists differ:

- **Operate** (client): **Overview, Paper Trading** only. Triangles,
  Scanner, and Opportunities as listed above are the triangular engine's
  own live-book/topology views — deployment-internal, not tenant data — and
  do not appear in the client console at all, not even gated. A tenant's
  "operate" surface is watching their own paper session and rule-driven
  Overview stats, nothing about the shared engine's internal book state.
- **Scanner Suite** (client): identical page set to the operator console
  (§2.2's seven Scanner Suite pages) — this group is the actual product a
  tenant is paying for, so it is not trimmed, only package-gated per §2.4
  (fewer venues/rules/strategies available, not fewer pages).
- **Portfolio** (client): **Portfolio & Balances, PnL & Analytics** only.
  Orders and Fills are the triangular engine's own order/fill ledger
  (`api.paper.orders(cycleID)` today, no tenant scoping exists or is
  planned for it) — out of the client console until/unless a tenant-scoped
  orders/fills read model is designed, which this document does not
  attempt.
- **Research, System** (client): dropped wholesale — Campaigns/Replay/AI
  Advisor/Exchanges/Markets/System Health/Audit Log/Telegram are all
  deployment-operational surfaces, not tenant-facing.
- **Control** (client): trimmed to **Alerts, Reports** — no Strategies/Risk
  Center, which are the operator's running-engine internals, not a
  tenant-facing control surface for a signals product.

### 2.3 Stats strip

Every Scanner Suite page (and Overview) carries a **stats strip**: a
single-row band of `Stat` cells directly under the page title, above the
filter card, reusing the existing `Stat` component. It is not a new
component — it's the existing `grid grid-cols-* gap-3` pattern from
Overview, formalised as a required region on every live-table page. Content
is page-specific (§6). Every strip cell that comes from a `PollState` uses
the existing `statOrError` degrade-one-cell pattern (Overview §2.1 of the
prior audit) so one failing upstream stat never blanks the page.

### 2.4 Three shades of "can't touch this" — not one

`ConsoleShell.tsx` today collapses two meanings into one grey span
(`title="Not implemented yet"` and `title="Requires OPERATOR or ADMIN"`
render identically). Package gating adds a third meaning. All three must
stay visually and textually distinct:

| State | Visual | Text | Click behavior |
|---|---|---|---|
| **Unbuilt** (no page exists yet) | grey text, `opacity-40`, `cursor-not-allowed` (unchanged from today) | tooltip: `"Not implemented yet"` | none — no navigation |
| **Role-restricted** (page exists, this role can't reach it) | grey text, `opacity-40`, `cursor-not-allowed` | tooltip: `"Requires {ROLE}+"` naming the actual minimum role, never just "restricted" | none |
| **Package-gated** (page/control exists, entitlement check fails) | grey text, `opacity-60` (lighter than the other two — this one is a sales surface, not a dead end), small lock glyph, package name chip (`🔒 Pro`) | tooltip/inline text: `"Included in {Package} — Upgrade"` | **clickable** — routes to the upgrade/billing page (client console only; the operator console never shows this state since it has every package) |

This applies at three levels, not just nav items: a nav entry, a filter
control within a page (e.g. a venue chip beyond the package's venue count),
and an action button (e.g. "Create rule" past the package's rule limit).
Same three-state component (`GatedControl`, new — §12) wraps all three.

**Package gating is display-only.** Every gated mutation is re-checked by
the backend's entitlement/limit check regardless of what the console
greys out — mirroring how `console-ux-audit.md` §6 frames the existing
`can()` frontend drift as a display bug, never a security hole. This
document does not set package limits (venue count, rule count, alert
channels, auto-paper strategy count) — those are the product manager's
numbers, sourced from Paddle product config. §5 (onboarding) and §6.1
(filter card) reference this same gating pattern for venue chips and rule
creation without inventing specific numbers; any count that appears in
either section is illustrative of the *mechanism*, not a proposed limit.

### 2.5 Notification centre

New bell icon in the top bar, right-aligned, badge = unresolved-and-unseen
count. Click opens a dropdown panel (not a full page):

- Up to 8 most recent items, newest first, each: severity badge (`severityTone`,
  four values, unchanged), one-line summary, relative time, source page link.
- CRITICAL items are pinned to the top of the panel regardless of recency
  and **never scroll out of the visible 8** — if more than 8 unresolved
  CRITICALs exist, the panel shows all of them and pushes lower-severity
  items out, with a footer line `"+N more — view all"`.
- **Opening the bell marks items "seen" (clears the badge count), never
  "acknowledged."** Seen/unseen is a notification-centre-only, client-side
  concept for badge hygiene; it has no effect on the alert's `active` /
  `acked` / `resolved` state on the Alerts page. Ack/Resolve stay
  exclusively on Alerts, unchanged from `console-ux-audit.md` §3.4 — the
  centre is read-and-triage, not the acknowledgement surface, so a CRITICAL
  alert cannot be silently cleared by a stray bell click.
- Footer: `"View all in Alerts →"`, always present, always the true source
  of record — the centre is a preview, never the only place a CRITICAL
  alert is visible (client-area.md: "critical alerts must never disappear").
- Feeds from the same `active` alert stream Alerts already polls/subscribes
  to (§1.15 of the prior audit) plus scanner-suite `screener_events` opens
  for rules with `telegram: true` — one panel, two upstream sources, merged
  client-side by time, each item tagged with its origin badge (`ALERT` /
  `RULE`).
- `aria-live="polite"` on the panel container so a screen-reader user
  notices new items while the panel is open; the bell badge count itself
  is not read live (would be too chatty) but is exposed via
  `aria-label="Notifications, {n} unseen"` on the bell button.

---

## 3. Data-age and stale-row treatment (shared canon)

One rule, defined once here, referenced by every table below instead of
restated per page. Source of the threshold: `scanner-suite.md` §3 — do not
invent a parallel one.

| Age relative to the row's poll interval | Tone | Cell text | Row treatment |
|---|---|---|---|
| ≤ 1× poll interval | `dim`/plain | exact age, e.g. `1.8s` | normal |
| > 1× and ≤ 3× poll interval | `warn` | exact age, e.g. `6.4s` | normal |
| > 3× poll interval | `bad` | **`STALE 11.2s`** — the word "STALE" is literal, always present, not implied by color | row background at `opacity-80` (a light reinforcement, deliberately a *different* value from §2.4's package-gated `opacity-60` so the two states never look identical at a glance — stale is `bad`-toned text plus a faint row dim, gating is a lock glyph plus a heavier dim) |

**Staleness is carried by text, not color alone**, for two reasons this
spec treats as binding: WCAG AA (color must never be the only signal) and
light-mode survival (light mode has no shipped tokens yet — §11 — and
grey-on-white is a much weaker signal than grey-on-dark; a design that
depends on background tint to say "don't trust this row" breaks the day
light mode ships). The `STALE {age}` text is the actual carrier in both
themes; the row dimming is a secondary reinforcement.

Applies to: Screener (`buy_age_ms`/`sell_age_ms`, independently — a row can
have one fresh side and one stale side, and both ages render, each with its
own tone), Perpetuals (`age_ms`), Funding (series freshness per venue),
Auto-Paper positions (`last_valuation_age`). Each page's stats strip also
carries a page-level "oldest row age" or "venues online" stat so the
operator doesn't have to scan the table to notice systemic staleness.

Every screener spread row additionally carries the model caption
**`no-transfer, top-of-book`** — shown once as a page-level footnote under
the table (it is a table-wide model constant per the wire contract, not a
per-row value) — and every network-status cell that has no public endpoint
renders the literal string **`unknown (venue requires API key)`**, never a
inferred open/closed guess. Both strings are contracted in
`scanner-suite.md` §7 and must not be reworded.

---

## 4. Row expand: drawer, not inline

`scanner-suite.md` §5 asks for "row expand for per-side quotes and the
calculator prefilled." The existing `VirtualTable` (`ui.tsx:174-250`)
hard-codes a 31px fixed row height and its own comment states rows "must be
single-line … for the fixed height to hold" — inline expand breaks that
windowing invariant the moment any table crosses the 500-row virtualization
threshold, which the Screener table will on day one (every quoted pair on
every venue).

**Decision: row expand renders as a right-hand detail drawer**, not an
inline row. Clicking a row (or pressing Enter on a focused row, §11)
opens a fixed-width (~420px) drawer sliding in from the right, overlaying
the table's right edge on desktop and full-screen on mobile (§8). The
underlying table is untouched — no row re-measurement, virtualization keeps
working exactly as built. This is the same shape `ConfirmDialog` already
uses (focus-trapped, `Escape` closes, `role="dialog"`), reused as a
non-modal panel (background stays interactive — an operator can keep
scanning the table while comparing one row's detail, so it does not use
`aria-modal="true"`, only `role="complementary"` with a labelled heading).

Drawer content (Screener): buy-side quote (venue, ask, ask qty, age),
sell-side quote (venue, bid, bid qty, age), the calculator pre-filled with
this row's base/quote/venues/size defaulted to the row's liquidity cap, and
a "Open in Calculator" link that navigates to `/calculator` with the same
query params for a full-page view. Drawer content (Perpetuals): spot/mark/
index trio, funding rate + predicted + interval + next funding countdown,
a small funding-history sparkline (last 24h, backend-supplied points only,
same no-client-side-computation rule as `HistogramChart`), and an "Open in
Funding" link.

Triangle and Opportunity detail already resolved this the *other* way — as
dedicated `[id]` pages (`web/src/app/triangles/[id]/page.tsx`,
`opportunities/[id]/page.tsx` already exist) rather than drawers, because
those rows carry enough content (leg waterfall, book depth, parameter
history) to want a full page and a shareable URL. The drawer is for
Screener/Perpetuals specifically because their expand content is a small,
disposable "check this one row" glance, not a destination — keep both
patterns, they answer different amounts of content, and do not try to
unify them into one mechanism.

---

## 5. Onboarding: first run

Trigger: a tenant's first login to the client console (or, on the operator
console, the first time no `screener_settings` version exists yet). Three
steps, presented as a wizard the operator can also reach later from
Settings — never a one-shot modal that vanishes if abandoned.

**Framing constraint that shapes every step below**: `screener_settings` is
its own versioned document (`internal/screener/settings.go`), separate from
`internal/platform.Settings` — nothing in `scanner-suite.md` states its
`field_timing`. This spec assumes venue enable/poll-interval/paper-balance
changes here are picked up by the poller/rule-evaluator on their next
cycle, not restart-scoped like the triangular engine's `platform.venues`/
`platform.paper` (settings-expansion.md §6) — **this is an assumption, not
a confirmed contract; flag to backend before build.** Regardless of which
way it resolves, the wizard renders whatever `field_timing` the apply
response actually returns per field, exactly like every other settings
surface in this codebase (never hardcode timing on the frontend) — so the
UI is correct either way; only the confirmation copy's specific wording
("takes effect within one poll cycle" vs. "takes effect on next engine
restart," reusing the existing `RestartBanner`) depends on the answer.

### Step 1 — Pick venues

- Grid of venue cards from `VenueTable()`/screener capabilities: name,
  availability badge, and for unavailable ones the verbatim backend reason
  (`"connector not built (T-050)"`) — never omitted, never paraphrased.
  Unavailable cards are non-interactive.
- Available cards are checkboxes. Selecting more than the tenant's package
  venue count disables further selection with the §2.4 package-gated
  treatment inline on the card (`🔒 Included in Trader — Upgrade`), not a
  blocking error after the fact.
- Copy: *"Public market data only — the screener never uses your exchange
  API keys, and this console never asks for one."* (ties to §1's non-goal).
- Primary button: **"Next: paper balances"**, disabled until ≥ 1 venue
  selected (screener has a floor of one venue — matching Screener's
  buy/sell filter needing at least one side).

### Step 2 — Set paper balances

- One row per (selected venue × asset) the tenant's chosen strategies will
  need inventory in (starts from a sensible default set — the quote assets
  common to cross-venue spot, e.g. USDT — with an "add asset" control).
- Every input is a decimal-string field with the same client-side format
  guard used elsewhere (numeric, no currency symbol, backend is the
  authority on precision) — never a float in the UI state either, per the
  platform-wide decimal-money rule.
- Inline note: *"These are simulated balances for automatic paper
  execution — no real funds are held or moved. You can change them later
  from Settings → Venues & fees."*
- Primary button: **"Next: create your first rule."**

### Step 3 — Create the first alert rule

- A small gallery of **our own** starter templates (not copied from any
  competitor's default set), e.g.: "Cross-venue spread ≥ 20 bps for 5s
  (Binance ↔ OKX)", "Funding carry ≥ 15% APR net", "Any qualified
  triangular cycle" — each a pre-filled rule form the operator can accept
  as-is or edit before saving (this doubles as the saved-template
  mechanism §6.5 uses everywhere else — a template is exactly a rule
  filter set with a name).
- Two toggles, both **off by default** and each with one sentence of
  consequence directly under it, not in a tooltip:
  - **Telegram push** — *"Sends this rule's alerts to your linked Telegram
    chat when it fires, subject to cooldown."* Package-gated per §2.4 if
    the tenant's package caps alert channels.
  - **Auto-paper execution** — *"Simulates this rule's trades automatically
    on paper and books the result to your paper ledger. This is not a
    guarantee of any return — see Evidence for this rule's own track
    record once it has run."* Off by default is deliberate: the operator
    opts in to automation with the consequence stated, not the other way
    round.
- Primary button: **"Finish setup."**

### Finish screen

Never claims the platform is "live" or "ready to trade" — it states what
was saved and what happens next, sourced from the apply response's
`field_timing`:

> **Setup saved as screener configuration v1.**
> Venues: {list}. Paper balances: {n} entries. Rule "{name}" created
> {Telegram: on/off}, {Auto-paper: on/off}.
> {Per-field timing line(s), verbatim from the backend — e.g. "Venues and
> balances take effect within one poll cycle." or, if it turns out to be
> restart-scoped, the existing `RestartBanner` treatment.}
>
> [Go to Screener] [Go to Alert Rules]

---

## 6. Pages

Every page: `ConsoleShell` active label, mode banner inherited from the
shell (unchanged), stats strip (§2.3), and — for Screener/Perpetuals/
Funding — a filter card (§6.1 shape, reused). RBAC per `scanner-suite.md`
§7: reads need `screener:view` (VIEWER+), settings/rule mutations need
`screener:config` (ADMIN); every mutating control is wrapped in the
existing `can()` hide pattern, backend remains the real gate.

### 6.1 Filter card (shared shape — Screener, Perpetuals, Funding)

A bordered card directly under the stats strip, above the table:

- **Venue chips**, two independent multi-select groups labelled "Buy on" /
  "Sell on" (Screener) or a single "Venues" group (Perpetuals/Funding) —
  chip = venue name, toggled by click, package-gated chips beyond the
  tenant's venue count render §2.4's lock treatment and are not togglable.
- **Numeric filters**: min spread (bps), min liquidity (quote currency,
  with the currency shown in the input's suffix, never assumed), min
  lifetime (seconds) — Screener; min carry APR — Perpetuals. Each input
  validates client-side (non-negative, sane upper bound) but the backend's
  own rejection always wins, per `console-ux-audit.md` §4.4.
- **Quote asset** select, **base allow/deny** as tokenized text inputs
  (type a symbol, Enter adds a chip, click × removes).
- **Template row**: a select of the operator's saved templates (populated
  from `GET /screener/templates`) + **"Save as template…"** (opens a small
  inline name field, not a full dialog — this is not a destructive action)
  + **"Reset filters"** (returns to the page's hard-coded defaults, not the
  last-saved template).
- Every filter change updates the table's query params (shareable/
  bookmarkable URL) and re-triggers the poll immediately, not on a debounce
  longer than ~300ms for text inputs (numeric/chip changes apply instantly).
- Collapsed-by-default on mobile (§8) behind a "Filters (n active)" toggle
  button that shows the active-filter count as a badge.

### 6.2 Screener (`/screener`) — T-069

**Stats strip**: Venues online (`x / y`), pairs tracked, spreads/s, oldest
row age (§3), poll interval.

**Table** (`VirtualTable`, since spot pairs × venues will exceed 500 rows):
`Base/Quote | Buy venue | Buy ask (age) | Sell venue | Sell bid (age) |
Spread net (bps) | Spread gross (bps) | Liquidity (quote) | Lifetime (s) |
Networks`. Column notes:

- Spread net/gross are the two separate backend fields, both shown — net is
  the primary sort/highlight column (bold), gross is secondary/dim, so an
  operator never mistakes gross for the tradeable number. Positive net
  spread uses `ok` tone text, not a filled cell background (red/green
  reserved for the number's sign only, per the platform's red/green
  principle — background color is not overloaded onto the same cell as the
  stale-row dimming from §3).
- Buy-age / Sell-age render as two small badges next to their respective
  venue name, independently toned per §3 (a row can be half-fresh,
  half-stale).
- Liquidity shows the quote-currency amount with the model caption
  (`no-transfer, top-of-book`) as the table footnote, not repeated per row.
- Networks cell: two badges (`withdraw {buy_venue}`, `deposit {sell_venue}`)
  — `open` (ok), `closed` (bad), `unknown (venue requires API key)` (dim,
  literal text, never a guess) per §3.
- Row click → detail drawer (§4).

**Empty**: *"No spreads match these filters. Loosen a threshold or clear
filters — 0 of {total pairs tracked} qualify."* (names the total so the
operator knows the table isn't broken, just filtered to zero).
**Loading**: `"Loading spreads…"`. **Error**: standard `ErrorBox`, with the
`ABSENCE_CODES` pattern extended for a screener-specific code if collectors
haven't started yet (`"screener_absent"` → *"The screener isn't running in
this deployment yet."*).

### 6.3 Perpetuals (`/perpetuals`) — T-069

**Stats strip**: Venues with perps online, contracts tracked, mean funding
interval, oldest row age.

**Table**: `Venue | Base | Spot mid | Perp mark | Basis (bps) | Funding
rate | Predicted funding | Interval | Next funding (countdown) | Carry APR
net | Carry APR gross | Age`. Carry APR net is the primary sort column,
same net-before-gross convention as Screener. "Next funding" renders as a
live countdown (`mm:ss`) computed client-side from the backend's
`next_funding_at` timestamp — arithmetic on a timestamp, not a
recomputation of any financial figure, so it does not violate the
never-recompute-money rule. Row click → detail drawer (§4).

**Empty/loading/error**: same pattern as §6.2, scoped to perpetuals data.

### 6.4 Funding (`/funding`) — T-069

Not a live table — a per-base, multi-venue **history** view:

- Base-asset selector (single select, defaults to the most recently viewed
  or first tracked base) + venue multi-select (reuses the filter-card
  venue chips, no spread/liquidity filters here since this page has no
  spread rows).
- Hours-back window picker (reuses the `WindowPicker` pattern already built
  for PnL & Analytics: 24h / 72h / 7d / 30d chips).
- One small multi-line chart per selected venue (new component,
  `FundingHistoryChart` — same hand-rolled-SVG convention as
  `PnLSeriesChart`/`HistogramChart`: backend-supplied `{at, rate}` points
  rendered directly, zero client-side computation beyond pixel geometry,
  `n =` sample count always shown, x-axis time labels, hover shows exact
  rate + timestamp). Charts stack vertically, one per venue, each labelled.
- Below the charts, a table of the same series (`Venue | At | Rate`) for
  operators who want exact values / copy-paste — charts are the glance,
  the table is the source.
- **Empty** (no base selected yet): *"Pick a base asset above to see its
  funding history."* **Empty** (base selected, no data): *"No funding
  history for {base} on the selected venues/window yet — funding accrues
  once per interval, so a freshly enabled venue may need up to one interval
  to show its first point."* (explains why zero data isn't a bug).

### 6.5 Calculator (`/calculator`) — T-069

A form, not a table:

- Inputs: base, quote, buy venue, sell venue, size (quote currency),
  transfer fee (quote currency, optional), an "override fees" expandable
  section (buy taker bps, sell taker bps — defaults to the venue fee
  table, editable for a what-if).
- Deep-linkable via query params (the Screener drawer's "Open in
  Calculator" link populates these) so a calculation is shareable/
  bookmarkable.
- Result panel, always net-first: **Net result** (quote currency, bold,
  toned by sign), **Net bps**, then the breakdown table (`Gross | Buy fees
  | Sell fees | Transfer fee | Net`) and a **Liquidity OK** badge
  (`ok`/`warn`) reflecting the backend's `liquidity_ok` boolean with the
  reason inline if false (e.g. *"size exceeds top-of-book on the sell
  side"* — verbatim backend reason, not a frontend guess).
- Every submit re-fetches `POST /screener/calculator`; nothing is computed
  client-side — the form is a request builder, the result panel renders
  the response verbatim.
- **Empty state before first submit**: the result panel shows a neutral
  placeholder, *"Enter a size and venues, then Calculate."* — never zeros
  that look like a real answer.

### 6.6 Alert Rules (`/scanner-alerts`) — T-069/T-070

Two regions: **Rules table** (top) and **Event history** (below, filtered
to the selected rule).

**Rules table**: `Name | Kind | Status | Venues | Threshold | Telegram |
Auto-paper | Cooldown | Actions`. Status is `enabled`/`disabled` (`ok`/
`dim`), not a lifecycle state — a rule doesn't "resolve." Threshold column
renders the kind-appropriate value (spread bps / carry APR / basis bps)
with its unit, never a bare number. Telegram/Auto-paper columns are small
badges (`on`/`off`), not raw booleans. Actions: **Edit**, **Duplicate**
(pre-fills a new-rule form from this one — the fastest path to "one more
rule like this but on OKX instead"), **Disable/Enable** (single click,
reversible, no confirm), **Delete** (confirm dialog, non-destructive-in-
data-terms since events history survives, but still asks — *"Delete rule
'{name}'? Its alert history stays on this page; the rule stops evaluating
immediately."*).

**Create/Edit form** (drawer, same shape as §4's row-detail drawer since a
rule edit is a comparably small, disposable task — not a separate page):
kind selector (spread/carry/basis) changes which threshold fields show;
venue multi-select (buy/sell for spread rules, single list for carry/
basis); quote/base allow-deny; cooldown (seconds); Telegram toggle;
Auto-paper toggle + paper size (quote currency) — same two consequence
sentences as onboarding step 3, not re-written per surface. Save uses the
shared `ConfirmDialog` + `DiffTable` pattern **only when editing an
existing enabled rule with auto-paper on** (a live-armed rule's threshold
change has real consequence for open positions); creating a new rule or
editing a disabled one saves directly, matching the existing "confirmation
scoped to genuinely consequential changes" principle from
`console-ux-audit.md` §4.3.

**Event history table**: `Opened | Closed | Base/Quote | Venues | Peak net
(bps or APR) | Lifetime (s) | Telegram sent | Auto-paper` — `Auto-paper`
column links to the Auto-Paper page's matching position/cycle when
`paper_execution_id` is present, else shows `— skipped ({reason}) —` in
dim text (never blank — a skip is information, not an absence).

### 6.7 Auto-Paper (`/auto-paper`) — T-069/T-071

Two regions: **Open positions** (top) and **Per-rule summary** (below) —
this second region is the shared component §1's table reuses for both the
operator's and client's Evidence pages, described once here.

**Open positions table**: `Rule | Strategy | Venues | Opened | Age | Net
PnL so far | Status`. Strategy is one of `CrossVenueSpot` / `Carry` /
`FuturesFutures` (verbatim internal names, shown with a one-line plain-
language gloss in a tooltip, not renamed — an operator debugging a
position needs the same vocabulary the backend logs use). Status: `open`/
`closing`/`stopped (maintenance margin)` for Carry positions specifically —
that last one is toned `warn`, not `bad`, since a modelled stop is expected
behavior, not a system error.

**Per-rule summary** (`RuleEvidenceTable`, new — shared with Evidence §6.8
below): `Rule | Alerts fired | Executed | Skipped (top reason) | Net PnL |
Hit rate | Mean lifetime | Sample size (n)`. **Sample size is always its
own column, never folded into a footnote** — a rule with `n=3` and a great
hit rate must look exactly as thin as it is; this table never lets a small
n hide behind a percentage (this is the same principle the codebase's own
`HistogramChart`/`Distribution` components already enforce with `n =`
labels, extended to a table). Skipped reasons expand on click into a small
inline breakdown (`liquidity: 4, data age: 2, no paper balance: 1`) rather
than a single collapsed number.

**Empty** (no rules with auto-paper on): *"No rule has automatic paper
execution enabled yet. Turn it on from **Alert Rules** to start building a
track record."* **Empty** (rule enabled, zero alerts yet): row still shows
with all-zero columns and `n = 0`, never omitted — an operator watching a
freshly enabled rule needs to see `0, 0, —, n=0`, not a missing row that
looks like the rule doesn't exist.

### 6.8 Evidence — T-080 (Phase 24)

The gate-facing rollup, distinct from both PnL & Analytics (per-triangle/
exchange/hour breakdowns of realized paper trading, operator console,
triangular-engine scope) and Reports (point-in-time generated documents).
Evidence is a **live, always-current view over the same auto-paper data**,
organized around the question "is this strategy's paper track record
positive, net of fees, at a real sample size, across regimes" — the
question the production-execution gate in
`docs/design/crypto-arb-platform-command.md` actually asks.

**Per-strategy cards** (one per strategy family — CrossVenueSpot, Carry,
FuturesFutures, Triangular — each a `Section`): reuses the
`RuleEvidenceTable` from §6.7 rolled up to strategy level, plus a
`PnLSeriesChart` of that strategy's cumulative paper PnL, plus three regime
tags (`calm` / `volatile` / `weekend`) each showing sample count in that
regime — regime classification is a backend-computed label on each cycle,
never inferred client-side from a date.

**Every number on this page cites its source**: a small `docs/campaigns/…`
path/link under each strategy card, pointing at the campaign report the
aggregate was drawn from — the non-negotiable "every published number
traceable to docs/campaigns/" applies here more than anywhere else on the
platform, since this is literally the page that decides whether the
production gate is met.

**Operator console only** — a gate checklist panel at the top, one row per
criterion (a)-(d) from the command doc, each **met / not met / n/a to this
console**, with the actual current value next to the threshold (`"≥ 30
days positive net PnL across calm/volatile/weekend: 14 days elapsed, net
PnL +$142.30 after fees, weekend regime n=0 — not yet met"`) — never a
single rolled-up "readiness score." (b) security review and (c) legal
decision render as document-link rows (`docs/decisions/…`) with a status
the operator sets manually elsewhere (this page reads that status, it does
not provide a checkbox to self-certify a legal decision — that stays a
deliberate, out-of-band act per the command doc's own framing).

**Client console**: the per-strategy cards only, no gate checklist, framed
as *"How this strategy has performed on paper, net of fees"* with a
permanent footer line: *"Simulated on paper. Past paper performance is not
a projection or guarantee of future results — see docs and the strategy's
own sample size above before relying on it."* This is the one sentence in
the whole client console doing the "never guaranteed" work explicitly, and
it appears once per strategy card, not buried in a single page-level
disclaimer an operator scrolls past.

**Empty** (strategy has zero auto-paper cycles): card renders with
`n = 0` and the sentence *"No paper cycles yet for this strategy — evidence
accumulates as rules with auto-paper enabled fire."*, never hidden.

---

## 7. Empty / loading / error / stale states — page-independent rules

Extends `console-ux-audit.md` §4.2, restated as the checklist every new
page above must pass:

1. Loading: `"Loading {what}…"` (existing `Loading` component), never a
   bare spinner.
2. Empty: names either the action that fills it or the precise reason,
   never an env var or shell command; where a real count exists (e.g. "0
   of 340 pairs match"), show it.
3. Error: routed through `ErrorBox`/`Unavailable` and the `ABSENCE_CODES`
   map (§6.2's `screener_absent` extends that map); the four-value status
   prefix (`Session required:`/`Forbidden:`/`Unavailable:`/`Error:`) is
   the only error vocabulary — no page invents its own prefix.
4. Stale: §3's `STALE {age}` text rule — never color alone, never omitted
   even when every row on the page is stale (the page still renders the
   stale rows, dimmed and labelled, rather than hiding them — a fully
   stale table is itself the signal that a collector is down, and hiding
   it hides that signal).
5. Degraded (partial failure): a page whose stats strip has one failed
   cell (`statOrError` pattern) still renders every other section — no
   single upstream failure blanks a whole Scanner Suite page.
6. Every WS/poll-driven live region carries its own visible connection
   state (`"WS connected/connecting/closed"` or `"Updated 2s ago"`),
   per the existing Scanner/Campaigns convention, extended to Screener
   and Perpetuals since both refresh fast enough that a silently-stopped
   poll is otherwise indistinguishable from "the market went quiet."

---

## 8. Mobile behaviour

Per this platform's own principle: "mobile: read-and-acknowledge works;
dense editing can remain desktop-first."

- **Read-and-acknowledge, mobile-first**: Notification centre, Evidence
  (client console — a client checking "is my strategy working" from a
  phone is the primary mobile use case), Alerts, Overview status strip.
  These reflow to single-column stat grids and full-width cards; the
  Evidence per-strategy cards stack vertically with the chart width
  clamped to the viewport.
- **Dense editing, desktop-first with a plain notice**: Alert Rules
  create/edit drawer, the Calculator's override-fees section, the
  onboarding wizard's Step 2 balance grid, Settings → Venues & fees. Below
  `md`, these show: *"Editing is easier on a wider screen — you can still
  view {the current rule / the active version} below."* rather than
  cramming the form (unchanged pattern from `console-ux-audit.md` §4.7).
- **Screener/Perpetuals tables** on mobile: the table itself stays
  desktop-first (a 10+ column dense table does not usefully reflow), but
  the **filter card and stats strip do** — filters collapse behind the
  "Filters (n active)" toggle (§6.1), and the table gets a horizontal
  scroll affordance with the first column (Base/Quote) sticky, so an
  operator can still glance at spreads from a phone even though editing
  filters is a two-tap affair rather than a one-glance one. The row-detail
  drawer (§4) already specs full-screen on mobile, which is the actual
  "read this one row's detail" mobile path — the read affordance is the
  drawer, not the raw table.
- **Icon rail** collapses to the existing hamburger + full-height overlay
  (`ConsoleShell.tsx:401-412`) below `md`, unchanged — the icon-rail/label-
  column split (§2.1) is a `≥ md` desktop refinement only; below `md` the
  overlay always shows full labels, never icon-only, since a touch target
  audit of an icon-only mobile nav is out of scope for this pass.

---

## 9. Accessibility (WCAG AA, keyboard)

Extends `console-ux-audit.md` §4.6:

- **Icon rail items** need `aria-label` naming the group (e.g.
  `aria-label="Scanner Suite"`) as their accessible name — a `title`
  attribute is not a substitute (no guaranteed exposure to assistive tech,
  no keyboard-focus visibility). Group collapse state exposes
  `aria-expanded` on the header button and `aria-controls` pointing at the
  item list's `id`; collapse state persists per-user (§2.1) but is always
  re-derivable from the DOM attribute, not just a visual chevron.
- **Row-detail drawer** (§4): `role="complementary"`, labelled by the
  row's identity (`aria-label="Detail: BTC/USDT Binance→OKX"`), focus
  moves to the drawer's close button on open, returns to the triggering
  row on close, `Escape` closes it — same keyboard contract as
  `ConfirmDialog` minus the modal focus trap (background stays reachable).
- **Table rows are keyboard-activatable as a unit** on Screener/Perpetuals
  specifically (opening the detail drawer) — this is the "future ... whole
  row focusable/Enter-activatable" item the prior audit deferred (§4.6,
  BL-23); it ships now because these two tables are the first to need
  row-level navigation beyond a single action button. Roving `tabindex`
  across rows, `Enter`/`Space` opens the drawer, arrow keys move focus row
  to row without leaving the table.
- **Package-gated lock glyphs** (§2.4) carry `aria-label="Included in
  {Package} — upgrade"` on the glyph itself, not just adjacent text, so a
  screen-reader user gets the reason in the same announcement as the
  control's disabled state.
- **Notification centre** live region per §2.5; the bell button's
  `aria-label` includes the live unseen count so it's announced on focus
  without needing the panel open.
- **Charts** (`FundingHistoryChart`, new — §6.4) ship an `aria-label`
  summarizing the series (`"Funding rate history, Binance, 72 hours, n=48
  points, range -0.01% to 0.04%"`) plus the underlying table (already
  specced in §6.4) as the accessible data source — a chart is never the
  only way to get a number off this page.
- **Focus-ring contrast**: carried over unresolved from the prior audit
  (BL-23) — every new interactive element introduced here (rail icons,
  filter chips, drawer controls, gated locks) must be included in that
  same contrast audit against both `--bg`/`--bg-panel` (dark) and their
  light-theme counterparts (§11) before ship, not verified twice.

---

## 10. Copy principles

Rules, not prose — every page above is written to satisfy these, and any
future Scanner Suite copy should be checked against them directly:

1. **Numbers first.** Net-of-fees values lead; gross is secondary and
   visually subordinate (dim, smaller, or a secondary column) — never the
   reverse, never equal visual weight. This applies to spreads (net before
   gross), carry (net APR before gross APR), and Evidence (net PnL leads
   every card).
2. **Every strategy/rule/position row shows both a net-of-fees number and
   a data age** (or, for Evidence, a "as of {report date}" / sample window)
   somewhere in that row or its immediate stats strip — no live number is
   ever presented without an age, and no financial row omits the fee-
   adjusted figure in favor of the gross one.
3. **Sample size travels with every rate/percentage.** Hit rate, carry
   APR, "profitable" language of any kind is never shown without its `n`
   in the same glance (same column, same card, same sentence) — never a
   footnote or a hover-to-reveal.
4. **Never "guaranteed."** Nor "risk-free," "certain," "always," or any
   synonym implying certainty — this extends past the literal word to the
   overall claim shape: a strategy card states what happened on paper, at
   what sample size, and stops there. The client-console Evidence footer
   sentence (§6.8) is the canonical phrasing to reuse verbatim wherever a
   performance number is shown to a client, rather than each page
   inventing its own hedge.
5. **Every published/aggregate number is traceable** — Evidence's
   `docs/campaigns/…` citation (§6.8) is the strongest form of this; every
   other page satisfies it more loosely by showing the backend's own
   number verbatim (never a frontend recomputation) with its `generated_at`
   or age visible.
6. **Own words only.** No competitor copy, icon set, or screenshot,
   anywhere in either console — this document's icon glyphs (§2.2), starter
   rule templates (§5 step 3), and every label above are original to this
   spec, not lifted from any reference product's UI.
7. **Empty states name an action or a precise reason, never a variable or
   command** (§7 restates this from the prior audit as still binding for
   every new page).
8. **Skipped/failed is information, not silence.** A skipped alert, a
   stopped position, a `n=0` row — all render explicitly with their reason,
   never as an absent row (§6.6, §6.7, §6.8 each restate this in context;
   it is the same rule every time: silence reads as "nothing happened,"
   which is different from and worse than "something happened and here's
   why it didn't convert.")

---

## 11. Light/dark

Light mode does not exist yet in the codebase (`console-ux-audit.md` BL-24:
`color-scheme: dark`, no light tokens, no `dark:` variants). This document
does not pick final hex values (UI-designer territory) but sets the
requirements every Scanner Suite component above must satisfy once the
token set ships, since these pages are the first the "light theme added to
the design system" line in `scanner-suite.md` §5 actually names:

- Every existing dark token (`--bg`, `--bg-panel`, `--bg-raised`, `--border`,
  `--text`, `--text-dim`, `--accent`, `--ok`, `--warn`, `--high`,
  `--critical`) gets a light-theme counterpart carrying the **same
  semantic meaning**, each pair meeting WCAG AA contrast (≥4.5:1 for text,
  ≥3:1 for UI-component boundaries like table borders and chip outlines)
  independently in its own theme — not by construction from the dark
  value, since a naive invert routinely fails contrast on mid-tones.
  `--high` in particular (already defined but unused per the prior audit)
  needs its light counterpart specified now, not deferred again.
- **Every state this document defines that currently leans on background
  tint (stale-row dimming §3, package-gated `opacity-60` §2.4) has a
  text/icon carrier that is theme-independent** — already designed that
  way above specifically so light mode is a token swap, not a redesign of
  any state.
- Red/green (net-spread sign, carry APR sign, PnL sign) keep their meaning
  in both themes but must be checked for the standard deuteranopia/
  protanopia confusion pair — every such value already carries a `+`/`-`
  sign and, where relevant, a `Badge` with text, per the existing
  "status conveyed by text/icon, not color alone" rule, so this is a
  contrast-tuning task, not a new requirement.
- Charts (`PnLSeriesChart`, `HistogramChart`, `FundingHistoryChart`) use
  CSS custom properties for every stroke/fill (already true of the two
  existing charts — `var(--accent)`, `var(--critical)`) so a theme switch
  repaints them for free; no chart hardcodes a color literal.

---

## 12. New components needed (delta against `web/src/components/ui.tsx`)

For the frontend implementer — everything else in this spec composes
existing primitives (`Stat`, `Table`, `VirtualTable`, `Badge`, `Button`,
`ConfirmDialog`, `DiffTable`, `Await`, `PnLSeriesChart`, `HistogramChart`,
`severityTone`, `statOrError`).

| Component | Purpose | Notes |
|---|---|---|
| `IconRail` + `NavGroupHeader` | §2.1 collapsible icon rail | Persists collapse state in `localStorage`; renders our own SVG glyph set (new, small, geometric — not copied) |
| `GatedControl` | §2.4 three-state grey wrapper | Wraps a nav item, filter chip, or button; props: `state: "unbuilt" | "role" | "package"`, `reason: string`, `upgradeHref?` |
| `RowDrawer` | §4 right-hand detail panel | Non-modal `role="complementary"`, focus-in/out per §9, full-screen on mobile |
| `NotificationBell` + `NotificationPanel` | §2.5 | Merges the alerts stream and screener rule-events stream; seen/unseen is local UI state only |
| `RuleEvidenceTable` | §6.7/§6.8 shared per-rule/per-strategy summary table | Always renders a sample-size column; skip-reason cells expand inline |
| `FundingHistoryChart` | §6.4 | Same hand-rolled-SVG, backend-values-only convention as the two existing charts |
| `FilterCard` | §6.1 shared filter shape | Venue chips, numeric filters, template save/load/reset — one component, three pages |

---

## 13. Open questions requiring confirmation before build

1. `screener_settings` field-timing (hot vs. restart-scoped) — assumed hot
   in §5; confirm with the backend before the onboarding wizard's finish-
   screen copy ships.
2. Exact package→limit table (venue count, rule count, alert channels,
   auto-paper strategy count per Starter/Trader/Pro/Desk/Enterprise) —
   product manager's numbers; this document only specifies the *display
   pattern* (§2.4), using illustrative placeholders.
3. Whether Evidence's regime tags (`calm`/`volatile`/`weekend`) are
   already a backend-computed label on paper cycles or need to be added —
   §6.8 assumes they exist; if not, this is a T-080 backend dependency to
   flag, not a UX decision.
4. Upgrade/billing route target for package-gated CTAs (§2.4) — depends on
   Phase 25's Paddle integration surface, not yet designed.
