# Operations Console UX Audit and Improvement Spec

Scope: `web/src/app/**/page.tsx`, `web/src/components/ConsoleShell.tsx`,
`web/src/components/ui.tsx`, `web/src/lib/api/client.ts`, `web/src/lib/auth.tsx`,
`web/src/lib/ws.ts`, `web/src/app/layout.tsx`, `web/src/app/globals.css`, and the
backend surfaces they call (`internal/api/*.go`, `internal/auth/rbac.go`,
`internal/strategy/params.go`, `internal/config/config.go`,
`internal/storage/authstore.go`).

Binding requirements referenced throughout: SKILL.md §30–§55 (web client area),
`.claude/skills/triangular-arbitrage-platform/resources/client-area.md`, and
`docs/deployment.md` §3b (console-driven recording and campaigns).

Operator context: single owner-operator (not a team). Explicit asks: "more
user friendly," and "manage everything from the UI" — symbols/starting
assets (today `ARB_SYMBOLS`/`ARB_STARTING_ASSETS`), fee/venue settings,
users & roles, recording & campaigns (shipped), strategy/risk params
(versioned config page exists), alerts, reports. The platform is paper-only;
live trading is permanently disabled (`ErrLiveTradingDisabled`) and every
surface must keep saying so.

Every finding below is anchored to `file:line`. Every backlog item states
whether the backend endpoint exists, is scaffolded (permission or storage
method defined but no route), or is entirely missing.

---

## 0. Priority rule

**P0** = violates a binding requirement from `client-area.md`/SKILL.md that is
already supposed to be true (mode banner, confirmation-before-danger,
severity-driven weight), or blocks the operator's stated ask with no
workaround. **P1** = operator explicitly asked for it (manage X from the UI)
and it is currently impossible without env vars/terminal/CLI. **P2** = SKILL.md
lists it as required console surface, but the operator did not raise it and
nothing currently misleads or blocks. Findings are tagged `[BL-nn]` pointing
at the backlog row in §5 that fixes them.

---

## 1. Page-by-page audit

### 1.1 Login — `web/src/app/login/page.tsx`

Clean, on-brand ("paper trading only — live execution permanently disabled"
at line 38). One real defect: the backend already distinguishes a
first-run/misconfigured-admin state — `internal/api/auth.go:68,128` return
`503 auth_unconfigured` / `"no users configured"` — but
`login/page.tsx:24` collapses every non-429 failure to the string
`"Login failed."`, discarding `err.apiError.code`. A fresh operator who has
not set `ARB_ADMIN_EMAIL`/`ARB_ADMIN_PASSWORD` sees the same message as a
typo'd password and has no path forward. `[BL-09]`

### 1.2 Overview — `web/src/app/overview/page.tsx`

This is supposed to be the "operations dashboard" (SKILL §32: system status,
today's counters, current exposure, exchange health, top triangles). Today it
renders four `Stat` cells (Mode, Version, Uptime, Components) from
`api.system.status()` and an apology paragraph ("Dashboard sections ... land
with their backing services" at line 63). It duplicates `Stat` locally
(lines 72–83) instead of importing the shared one from `ui.tsx`, uses a
hand-rolled `LoadState` instead of `usePoll`/`Await`, and its error prefix is
`"Degraded:"` (line 45) where `ErrorBox` elsewhere says
`"Error:"/"Session required:"/"Forbidden:"/"Unavailable:"` — two different
error vocabularies in the same app. There is no recorder status, no campaign
status, no alert count, no paper P&L, and no quick actions (start recording,
run campaign, pause paper, view alerts) — exactly the elements the operator
needs to answer "is everything OK" in one glance. `[BL-01, BL-25]`

### 1.3 Scanner — `web/src/app/scanner/page.tsx`

Solid: live counters via poll, a genuine WS-driven live stream (`connectHub`),
and a persisted recent-opportunities table, each using the shared kit
correctly. No mode banner (see §1.16). No saved views/filters/pause-display/
pin/export controls SKILL §33 asks for — acceptable for a single operator,
P2. `[BL-30]`

### 1.4 Triangles — `web/src/app/triangles/page.tsx`

Reasonable: topology summary + quality score table with a time-window
selector. No enable/disable per triangle (SKILL §35 requires it) and no
click-through to a triangle detail page (SKILL §34 — visual graph, leg
waterfall, book depth, parameter history) because that page doesn't exist.
`[BL-26]`

### 1.5 Opportunities — `web/src/app/opportunities/page.tsx`

Status filter + persisted-history table. `reason_code` is shown as a raw
backend code (e.g. whatever the risk engine emits) with no human-readable
expansion and no row drill-down into "why qualified/rejected... which book
versions were used" (SKILL §36). No opportunity detail page exists.
`[BL-27]`

### 1.6 Paper Trading — `web/src/app/paper/page.tsx`

Engine stat grid + Pause/Resume, gated correctly by `can(role,
"paper:control")`. Two gaps:
- No **Reset** control anywhere, despite SKILL §37 explicitly requiring
  "reset ONLY with strong confirmation." `PermPaperReset` is already defined
  in `internal/auth/rbac.go:17` and granted to `RoleAdmin` (rbac.go:51), but
  no route exists (`internal/api/server.go` has no `/api/v1/paper/reset`)
  and no button exists in the UI. An operator who wants a clean paper
  session has no console path to get one. `[BL-10]`
- Empty state at `paper/page.tsx:80`: `"persisted cycles (requires
  ARB_DATABASE_URL)"` — an environment variable name in end-user copy.
  `[BL-16]`

### 1.7 Portfolio — `web/src/app/portfolio/page.tsx`

Balances + exposure + PnL-by-asset, correctly separates "unmarkable
exposure" with a warning tone. This page silently stands in for both
"Portfolio" and "Balances" from SKILL §31's nav list, and there is no
dedicated "PnL & Analytics" page with breakdowns by exchange/triangle/
market/hour/config-version or the required charts (SKILL §41) — the current
per-asset table is the entire analytics surface. `[BL-19]`

### 1.8 Exchanges — `web/src/app/exchanges/page.tsx`

Read-only feed health + order-book state table, labeled correctly as public
market data only. No configuration surface at all (SKILL §42 asks for
enabled/paper-enabled/markets/starting-assets/fee-tier/limits) — entirely
read-only today, which is honest but is exactly the "Venues & fees" gap the
operator named. `[BL-15]`

### 1.9 Strategies — `web/src/app/strategies/page.tsx`

The most functionally complete page (versioning, hot-swap, rollback,
RBAC-gated edit) and also the clearest case of a **frontend-only** gap
hiding a fully-built backend:
- Editing is a raw JSON `<textarea>` over the entire `StrategyParams` blob
  (lines 85–90). `internal/strategy/params.go` defines fully typed
  `ScannerParams`/`RiskParams`/`NotificationParams` with named, bounded
  fields and a `Validate()` that returns specific range errors (e.g.
  `"strategy: risk.max_drawdown out of (0,1)"`, `params.go:176`). None of
  that structure or those bounds reach the UI — an operator must know the
  JSON schema and guess the valid ranges, and a typo produces the backend's
  raw Go-flavored error string as the entire feedback (`apply()` at
  `strategies/page.tsx:34-39` just surfaces `err.message` verbatim).
  `[BL-14]`
- `ConfigVersion.diff` (`client.ts:215`, shape
  `Record<string, {old, new}>`) is fetched but only its **keys** are shown —
  `Object.keys(v.diff).join(", ")` at `strategies/page.tsx:114` — the actual
  before/after values the backend already computed are discarded. This
  directly contradicts the binding requirement in `client-area.md:26-27`:
  "config change ... use explicit confirmation with before/after diffs."
  `[BL-03]`
- **No confirmation step at all.** `Apply` (line 92) and `Roll back to`
  (line 116) both fire the mutation on a single click. There is no
  confirmation dialog anywhere in `web/src/components` (verified: no
  `dialog`/`modal`/`confirm` component exists in the codebase). A misclick
  applies a new strategy/risk version to the running scanner immediately.
  `[BL-02]`
- Symbols and starting assets are **not here** — they aren't part of
  `strategy.Params` at all; they live in `internal/config.go:58-59` as
  boot-time env (`ARB_SYMBOLS`, `ARB_STARTING_ASSETS`), invisible to this
  page or any other. `[BL-13, BL-13b]`
- **OPERATOR gets a 403 dead end, not a hidden control.** The whole editor
  is gated on `can(role, "scanner:config")` (`strategies/page.tsx:53`), so
  an OPERATOR sees "Edit draft" and can open and edit the full JSON blob,
  including the `risk` section. But
  `internal/auth/rbac.go:72-77`'s `PermissionForConfigSection` routes any
  change touching `risk.*` to `PermRiskConfig`, which only ADMIN holds
  (`rbac.go:51`) — an OPERATOR only discovers this on Apply, after editing,
  via a raw backend error. The page-level gate is coarser than the
  backend's per-section gate. `[BL-14]`
- **The form would collapse two different effect-timing guarantees into
  one "applies immediately" promise.** `params.go:40`'s own comment flags
  `scanner.workers` as applying "at component start, not on hot swap,"
  unlike every other field in `Params`, which the running config service
  hot-swaps. A field-level form (`[BL-14]`) that doesn't distinguish these
  will silently mislead the operator into thinking a `workers` change took
  effect when it didn't.

### 1.10 AI Advisor — `web/src/app/ai/page.tsx`

Good approve/reject flow with role gating and honest copy about the risk
engine never being overridden. Same missing-confirmation issue as
Strategies: `decide()` at line 19 fires `api.ai.approve`/`reject` on a single
click, with no before/after preview of what approval will change in the
active config (an approval writes a new config version — the operator sees
only "Recommendation approved." afterward, then must go to Strategies to see
what changed). `[BL-02]`

### 1.11 Risk Center — `web/src/app/risk/page.tsx`

Read-only, as it should be per SKILL §46 ("Risk thresholds can be changed
only by authorized roles" — via Strategies, not here). Limits, circuit
breakers, and rejection-reason counts are all shown; breaker `State` badge
tone correctly maps OPEN→bad/HALF_OPEN→warn/CLOSED→ok. No "risk event
timeline" (SKILL §46) beyond the session-scoped rejection counter — history
resets on process restart. P2. `[BL-31]`

### 1.12 Replay & Backtesting — `web/src/app/replay/page.tsx`

Recordings table + config-version diff comparator (this page's diff table
*does* show before/after values, unlike Strategies — inconsistent pattern
within the same app). The **primary call to action for the whole page** is a
literal shell command rendered as UI: `replay/page.tsx:95-97` renders
`ARB_MODE=REPLAY ARB_REPLAY_SESSION={id} ./arbd` inside a `<code>` block as
the "Replay command" column, and the page's own copy admits it: "Runs start
from the CLI today; in-console runs arrive with the backtest worker." This
is the single clearest instance of the operator's complaint — the console
shows you the terminal command instead of doing the thing. `[BL-16, BL-17]`

This page and Campaigns (§1.13) both independently poll and render "Recorded
sessions" from the same `market_recording_metadata` data, with different
columns and no link between them. `[BL-17]`

### 1.13 Campaigns — `web/src/app/campaigns/page.tsx`

The newest and best-designed page: WS-driven recorder status, a real launch
form, a merged poll+WS run list, and a verdict panel that renders backend
flags verbatim with `BAD_PHRASES` highlighting. Issues:
- **Verdict severity is string-matched in the frontend.**
  `campaigns/page.tsx:16` hardcodes
  `["PROFITABLE ONLY UNDER PERFECT CONDITIONS", "UNPROFITABLE", "NO
  CYCLES"]` and `flagsTone()` (line 57) returns `"ok"` (green border) when
  `flags` is `undefined` — i.e. "no verdict computed yet" renders
  identically to "verdict is good." If a future backend flag uses different
  wording, it silently renders as neutral text with no color signal. The
  severity should be a field the backend emits, not a phrase the frontend
  greps for. Fixing the `undefined` → `ok` default and adding the Verdict
  column (below) is frontend-only and should ship now; moving the severity
  itself off string-matching needs a small backend addition. `[BL-05a,
  BL-05b]`
- **The Runs table has no verdict column.** An operator must click "View" on
  every row to discover a run said "UNPROFITABLE" (lines 429–445 list ID,
  Recording, Status, Progress, Started, Finished, Actor — no flag summary).
  `[BL-05a]`
- **"Run campaign" doesn't run a campaign.** The button at line 306–311
  calls `prefillRun`, which only sets the recording dropdown and scrolls to
  the form (lines 159–162) — the operator still has to press "Launch
  campaign" a second time below. The label promises a completed action.
  `[BL-05a]`
- **A stuck run permanently disables the feature.** `runInProgress` (line
  146) is true whenever any run is `queued` or `running`, and disables both
  "Run campaign" and "Launch campaign" globally. There is no cancel
  endpoint (`internal/api/opsapi.go` has no `DELETE`/cancel route for
  `/api/v1/campaigns/{id}`) and no UI escape hatch — a crashed or hung
  worker run blocks all future campaigns until a database row is edited by
  hand. `[BL-08]`

### 1.14 Reports — `web/src/app/reports/page.tsx`

Straightforward generate + list + JSON detail view. Two RBAC bugs plus a
gap against what the operator explicitly asked to manage from the UI
("reports"):
- `reports/page.tsx:30-31` renders "Generate daily now"/"Generate weekly
  now" for **every** authenticated user, with no `can(role, ...)` check —
  a VIEWER sees both buttons and gets a 403 on click (backend correctly
  gates `PermReportGenerate` at `internal/api/reportsapi.go:32`, but the
  frontend never hides it).
- Even if the page did call `can()`, it would still be wrong:
  `auth.tsx:95` computes OPERATOR's permissions as
  `operator.has(perm) || perm.startsWith("view:") || perm === "reports:view"`
  — the string `"reports:generate"` matches none of those branches, so
  `can("OPERATOR", "reports:generate")` returns `false`, while
  `internal/auth/rbac.go:44` grants OPERATOR `PermReportGenerate` on the
  backend. The frontend's permission mirror has drifted from the backend
  matrix it's supposed to shadow. Note every other permission in `can()` is
  already correct — ADMIN short-circuits to `true` (`auth.tsx:94`), so this
  is a single missing string, not a systemic gap. `[BL-06]`
- Report detail is a raw `JSON.stringify(selected, null, 2)` dump (line 56)
  — no formatted view of executive summary / sections despite `Report`
  already having typed fields (`kind`, `period_start/end`,
  `executive_summary`, `recommended_actions`, `notes`), and no download at
  all — SKILL §48 requires CSV where appropriate, structured JSON, and a
  print-friendly view; today there is no way to get a report out of the
  browser. `[BL-32]`

### 1.15 Alerts — `web/src/app/alerts/page.tsx`

Filter tabs, ack/resolve actions, correct RBAC gating. The severity system
is incomplete at the shared-component level, not this page — see §1.17.
Resolved alerts remain in the list (good — matches "critical alerts must
never disappear merely because resolved," SKILL §49), but there's no visual
distinction between an alert that auto-resolved vs. one an operator resolved,
and no per-severity sound/browser-notification for CRITICAL while the tab is
backgrounded. P2. `[BL-33]`

### 1.16 System Health — `web/src/app/system/page.tsx`

The weakest page in the console. It bypasses the shared kit entirely: no
`usePoll` (single `useEffect`, fetches once on mount and never again — every
other page polls every 3–15s), no `Await`, a hand-rolled error path that
loses `ApiError.status`, and the entire body is `JSON.stringify(status,
null, 2)` (line 26) of the same four fields Overview shows. Its own comment
admits it: "Full health payload ... arrives with observability task T-035"
(line 30). None of SKILL §50's required fields (CPU, memory, goroutines, GC,
DB connections, queue depths, messages/sec, per-exchange feed
latency/reconnects/sequence errors) are present. `[BL-18, BL-25]`

### 1.17 Audit Log — `web/src/app/audit/page.tsx`

Entity-filter text input + table. Functionally fine. Empty state at line 29:
`"audit events (requires ARB_DATABASE_URL)"` — same environment-variable
leak as Paper and Reports. `[BL-16]` Section title says "requires
OPERATOR+" (line 24) but the page itself does no role gating or messaging
for a VIEWER who can't reach it — VIEWER isn't granted `PermViewAudit`
(`rbac.go:36-39`), so they'd 403 with no explanation; the nav item itself
should be hidden or annotated for VIEWER. P2. `[BL-34]`

### 1.18 Settings — `web/src/app/settings/page.tsx`

This is meant to be the answer to "manage everything from the UI," and today
it is three static sections: Session (sign out), Process (four stats
duplicated from Overview/System), and a bullet list titled "Security
posture" that is candid but is pure prose, including the operator's exact
blocker written as an apology: `settings/page.tsx:46` — "User management
(create/disable users, role changes) is not built yet — the bootstrap admin
is configured via environment." There are no Markets & assets, Venues &
fees, Users & roles, Strategy & risk, or Notifications sections — Settings
does not yet contain a single one of the five groupings the operator asked
for; Strategy & risk lives on a separate "Strategies" nav item instead.
`[BL-12]`

### Cross-cutting: navigation — `web/src/components/ConsoleShell.tsx`

`NAV` (lines 6–24) lists 17 items, and the comment at lines 4–5 promises
"Sections without a page yet render as disabled entries — the console never
pretends a page exists," with dead code at lines 50–58 to render exactly
that (greyed-out, `cursor-not-allowed`, `title="Not implemented yet"`). But
every single entry in `NAV` already has an `href` — the disabled branch
never executes. Seven pages SKILL §31 requires are not in `NAV` at all, not
even as disabled placeholders: **Orders, Fills, Balances, PnL & Analytics,
Markets, Telegram, Users & Security**. The operator has no way to discover
these are planned-but-missing versus never-planned; they simply don't exist
in the sidebar. This is worse than the disabled-entry pattern the code
already built for exactly this situation. `[BL-07]`

### Cross-cutting: mode banner — required, absent

`client-area.md:24-25` (binding): "Mode banner (MARKET_DATA/RECORD/REPLAY/
BACKTEST/PAPER/SHADOW) always visible; PAPER is clearly labeled everywhere
P&L appears." Verified absent: `web/src/app/layout.tsx` renders only
`AuthProvider` with no banner; grepping `web/src` for
`MARKET_DATA|SHADOW|BACKTEST` returns only the literal shell command on the
Replay page. `ConsoleShell.tsx:32-34` renders a **static** string
("paper trading only") in the sidebar subtitle, not the live mode from
`api.system.status().mode`. The live mode value only appears as one `Stat`
cell each on Overview and Settings, on their own poll cadence, nowhere else.
An operator mid-REPLAY or mid-BACKTEST session has no persistent visual
signal of that fact while looking at Scanner, Paper, or Portfolio numbers.
`[BL-01]`

### Cross-cutting: severity vocabulary — `web/src/components/ui.tsx`

`severityTone()` (lines 136-140) only branches on `"CRITICAL"` and
`"WARNING"`; every other value — including `HIGH`, which SKILL §49 defines
as a distinct severity between WARNING and CRITICAL — falls into the
default `"dim"` (grey, same as INFO). `globals.css:14` defines
`--high: #f0713a` and it is never referenced by any component in
`web/src`. A HIGH-severity alert is visually indistinguishable from an INFO
one today. `[BL-04]`

### Cross-cutting: dark/light mode — `web/src/app/globals.css`

`html { color-scheme: dark }` (line 19) with a single `:root` token block
and no `@media (prefers-color-scheme: light)` override, no light-mode token
set, and no Tailwind `dark:` variants anywhere in `web/` (verified by grep).
Light mode does not exist, not "is degraded" — this is a gap against this
spec's own principle that light mode must remain usable, not a SKILL.md
requirement, so it's scoped P2 unless the operator raises it. `[BL-24]`
(BL-24 covers both the light-mode token set and the responsive-collapse
work below — see its backlog row for the two-part scope.)

---

## 2. Proposed information architecture

Keep the flat sidebar (appropriate for a single operator — no need for
collapsible mega-groups), but reorganize into five visually grouped
sections with a persistent mode banner above them all, and stop hiding
planned-but-unbuilt pages.

```
┌────────────────────────────────────────┐
│ ARB CONSOLE                             │
│ [● PAPER]  live trading permanently off │  ← mode banner, always visible,
├────────────────────────────────────────┤     color-coded per mode (§4.1)
│ OPERATE                                 │
│   Overview                              │  ← new: real dashboard, §2.1
│   Scanner                               │
│   Triangles                             │
│   Opportunities                         │
│   Paper Trading                         │
│                                          │
│ PORTFOLIO                               │
│   Portfolio & Balances                  │  ← renamed, covers both nav items
│   PnL & Analytics                       │  ← new page, BL-19
│   Orders                                │  ← new page, BL-20
│   Fills                                 │  ← new page, BL-20
│                                          │
│ RESEARCH                                │
│   Campaigns                             │  ← recorder + campaigns (current)
│   Replay & Backtesting                  │
│   AI Advisor                            │
│                                          │
│ CONTROL                                 │
│   Risk Center                           │
│   Alerts                                │
│   Reports                               │
│                                          │
│ SYSTEM                                  │
│   Exchanges                             │
│   System Health                         │
│   Audit Log                             │
│   Telegram                              │  ← placeholder, disabled, BL-07
│                                          │
│ Settings ⚙                              │  ← pinned at bottom, own icon,
│                                          │     always visible regardless of
│                                          │     group scroll (§2.2)
└────────────────────────────────────────┘
```

Every item without a shipped page renders through the **existing** disabled-
entry branch (`ConsoleShell.tsx:50-58`) instead of being omitted — that code
already does the right thing, it's just unused. `[BL-07]`

### 2.1 Overview — real operations dashboard

Replace the four-stat page with, top to bottom:

1. **Status strip** (reuses the mode banner's data source, one poll): Mode,
   Scanner ready/not, Paper engine running/paused, Recorder
   recording/idle, Alerts unresolved count (link to Alerts), DB
   connected/degraded.
2. **Today** stat grid: opportunities detected, qualified, rejected, paper
   cycles started/completed/failed, net paper P&L, fees, max drawdown — all
   already computable from `api.scanner.status()`, `api.paper.cycles()`,
   `api.pnl()`; no new backend needed for a first cut.
3. **Current** stat grid: active simulations, capital reserved/available,
   average net edge (existing `api.risk()`/`api.portfolio()` data).
4. **Exchange health** mini-table: reuses `api.system.health()` (already
   powers the Exchanges page).
5. **Recent campaign verdicts** (last 3 runs from `api.campaigns.list()`)
   with the verbatim flag headline and its color — surfaces "PROFITABLE
   ONLY UNDER PERFECT CONDITIONS" on the home page instead of three clicks
   deep.
6. **Quick actions** row: "Pause paper engine" / "Resume", "Start
   recording" / "Stop", "View unresolved alerts (N)" — each a thin wrapper
   around an existing mutation, RBAC-gated with `can()` exactly like the
   source pages.

All data for a v1 of this page already exists behind current endpoints;
it is a frontend-only assembly job. `[BL-01]`

### 2.2 Settings — five sections, per the operator's own list

Restructure `settings/page.tsx` into a sectioned page (tabs or anchored
sections — given five sections and a desktop-first dense console, anchored
sections with a sticky in-page sub-nav reads better than tabs, since an
operator often wants to scan more than one section at once):

- **Markets & assets** — today: read-only panel listing configured symbols/
  triangles/starting assets from `api.scanner.status()` (`markets`,
  `triangles` are already returned). Editable version is `[BL-13b]`
  (backend-missing, see §5).
- **Venues & fees** — today: read-only exchange list (reuse Exchanges page
  data) plus an explicit "not yet editable from the console — no backend
  endpoint exists" state rather than hiding the section. `[BL-15]`
- **Users & roles** — user table (email, role, status, last login) +
  invite/disable/change-role actions. Backend-missing: `[BL-11]`.
- **Strategy & risk** — links to (or embeds) the rebuilt structured
  Strategies page (`[BL-14]`), keeping versioning/rollback/diff there since
  it's already a full page.
- **Notifications** — structured form for `NotificationParams`
  (cooldown seconds, per-severity → channel routing) instead of being
  buried inside the Strategy JSON blob; still writes through
  `api.config.apply()` since it's part of the same `StrategyParams`
  document server-side.
- **Session** and **Security posture** stay, trimmed to remove the
  now-resolved bullet about user management once `[BL-11]` ships.

---

## 3. Guided flows (exact copy)

### 3.1 First-run setup

Trigger: `POST /api/v1/auth/login` returns `503 auth_unconfigured` (already
implemented, `internal/api/auth.go:68`).

Login page, replacing the generic failure branch:

> **No admin account is configured yet.**
> Set `ARB_ADMIN_EMAIL` and `ARB_ADMIN_PASSWORD` in the deployment
> environment and restart the API, then sign in here. This is the one setup
> step that still requires a deploy-time variable — see
> [docs/deployment.md §1](../deployment.md).

Rationale: this is a genuine deploy-time bootstrap constraint (the process
needs *a* first credential before any UI can exist to create one) — the fix
is not to fake a self-service flow the backend can't support, it's to say
precisely and only this much, once, and never make the operator guess why
login is failing. Every other `ApiError` keeps today's generic
`"Login failed."` (do not leak whether an email exists — enumeration risk).

Once signed in as the bootstrap ADMIN, Overview's quick actions and
Settings → Users & roles become the path for every subsequent user; no
second env-var account should ever be needed. `[BL-09, BL-11]`

### 3.2 Record → run campaign → read verdict

This flow already exists end-to-end per `docs/deployment.md` §3b; the spec
below tightens copy and fixes the two dead ends found in §1.13.

1. **Campaigns → Recorder card → "Start recording."** Button disables
   immediately, label changes to "Starting…". On success, State flips to
   `● RECORDING` (green) and Session/Uptime/Frames populate live over the
   WS `recordings` topic.
2. Operator lets it run, then **"Stop recording."** Confirmation (this is a
   safe, reversible action — no modal needed, but disable the button while
   the in-flight last segment closes and show: *"Stopping — waiting for the
   last segment to close…"* per the real backend behavior described in
   deployment.md §3b, rather than appearing to hang.)
3. **Recorded sessions table → "Run campaign"** on a closed (`ended_at` set)
   row. This **scrolls to and prefills** the launch form — rename the
   button to **"Configure campaign…"** so its actual behavior (open the
   form, don't launch) matches its label. Keep a distinct **"Launch
   campaign"** button in the form as the actual trigger, unchanged.
4. On launch: `"Run <id> queued."` — already correct — then the Runs table
   updates over WS with live progress (`done/total — step`).
5. **Runs table gets a Verdict column**, populated as soon as `flags` is
   present, using the backend's own headline (no frontend phrase list):

   | ID | Recording | Status | Progress | Verdict | Started | Finished | Actor |
   |----|-----------|--------|----------|---------|---------|----------|-------|

   Verdict cell shows the single worst-severity flag headline verbatim
   (e.g. `PROFITABLE ONLY UNDER PERFECT CONDITIONS`) in the tone the
   backend assigns it (§5, BL-05); blank/pending runs show `— pending —` in
   dim text, never a colored "ok" placeholder. **This cell wraps to
   multiple lines within its column — it never uses the `max-w-* truncate`
   + `title=` pattern the codebase reaches for elsewhere (`reports/
   page.tsx:44`, `alerts/page.tsx:59`, `ai/page.tsx:53`) and never hides the
   text behind a tooltip.** A verdict an operator has to hover to read is a
   verdict that's effectively hidden.
6. **"View"** opens the full report. The Verdict panel already renders
   flags verbatim and unsummarized (`campaigns/page.tsx:449-484`) — this is
   correct and must not change: **never truncate, summarize, or hide a
   verdict behind a "show more," a tooltip, or a `title=` attribute.**
7. If a run is stuck `queued`/`running` past a reasonable bound (e.g. no
   progress delta for 10 minutes), show inline under the Runs table:

   > **Run `<id>` has not progressed in over 10 minutes.** It may have
   > crashed. Campaign controls are disabled while a run is in progress —
   > contact an ADMIN to clear it directly, or wait for `[cancel endpoint,
   > BL-08]` once available.

   This doesn't fix the missing cancel endpoint, but it stops the console
   from silently locking up with no explanation.

### 3.3 Config change with versioning/rollback (Strategy & risk)

Replace the raw-JSON textarea flow with a structured form (§5, BL-14) and
add the confirmation step client-area.md already mandates:

1. Operator edits a field, e.g. `risk.max_drawdown` from `0.05` to `0.10`.
   Inline validation mirrors `params.go`'s bounds live (no round-trip
   needed for range checks): *"Must be greater than 0 and less than 1
   (100%)."* if out of bounds.
2. **"Review changes"** button (replaces the immediate "Apply as new
   version") opens a confirmation dialog:

   > **Apply new strategy configuration?**
   > This becomes version **v13**. Fields marked **immediate** take effect
   > in the running scanner as soon as you confirm; fields marked **on
   > restart** are saved to v13 now but only take effect the next time the
   > engine process starts.
   >
   > | Parameter | Current (v12) | New (v13) | Effect |
   > |---|---|---|---|
   > | risk.max_drawdown | 0.05 (5%) | 0.10 (10%) | immediate |
   > | scanner.workers | 2 | 4 | on restart |
   >
   > [Cancel]  [Apply v13]

   The diff table is populated from the same before/after values the
   backend already returns in `ConfigVersion.diff` — today thrown away
   (§1.9) — or, for an in-progress unsaved edit, computed client-side by
   diffing the draft against the active version's `params`. **Every row
   carries its effect-timing tag** (§5, BL-14): every field in `Params` is
   hot-swapped immediately except `scanner.workers`, which
   `params.go:40`'s own comment documents as applying only "at component
   start" — the form must not claim a change took effect when it didn't.
3. On confirm: `"Version v13 active."` (existing copy, kept). On validation
   failure from the backend (belt-and-suspenders past client-side checks):
   show the backend's `message` verbatim under the field it names, not just
   as a page-level banner — `params.go`'s errors already name the exact
   path (`"strategy: risk.max_drawdown out of (0,1)"`), so the form can
   parse the trailing path segment and highlight that field.
4. **Rollback** gets the same confirmation shape:

   > **Roll back to v11?**
   > This creates a new version, **v14**, with v11's parameters. v12 and
   > v13 remain in history and can be rolled back to again later.
   >
   > | Parameter | Current (v13) | Restoring (v11) |
   > |---|---|---|
   > | risk.max_drawdown | 0.10 (10%) | 0.05 (5%) |
   > | scanner.ttl_ms | 400 | 350 |
   >
   > [Cancel]  [Roll back to v11]

AI Advisor approvals get the identical confirmation shape, since approval
also writes a new config version:

> **Approve recommendation: risk.min_net_edge_bps 5 → 8?**
> Reason: *<r.reason verbatim>*. This applies immediately as a new
> config version, auditable exactly like a manual change.
>
> [Reject instead]  [Cancel]  [Approve]

`[BL-02, BL-03, BL-14]`

### 3.4 Alert acknowledgement

Current flow (filter tabs, Ack/Resolve buttons) is close to right; tighten
severity handling and add the HIGH tone fix:

1. New CRITICAL alert arrives over WS → appears at the top of "active,"
   badge `CRITICAL` in `--critical` red, and (new) the mode-banner area
   shows a small persistent counter badge (e.g. a red dot with count) so a
   CRITICAL alert is visible even when the operator isn't on the Alerts
   page — reusing the same status-strip mechanism as §2.1.
2. **Ack**: single click, no confirmation (non-destructive, reversible by
   re-filtering) — badge moves to `acked`, count updates. Copy: no change
   needed, `"Ack"` is fine and matches Telegram's inline-button vocabulary
   per SKILL §57.
3. **Resolve**: single click, no confirmation for WARNING/HIGH; for
   CRITICAL, one extra step given the SKILL §49 requirement that critical
   alerts "must never disappear" — not a blocking modal, but an inline
   confirm-in-place: the Resolve button becomes `"Confirm resolve"` for 3
   seconds (matches a common low-friction pattern for a single operator who
   doesn't need a full modal for every critical resolve, while still
   preventing a stray double-click from silently closing a critical issue).
4. Resolved alerts stay in the list with `state: resolved` and their full
   history (first_at/last_at/count) — unchanged, already correct.

`[BL-04]`

---

## 4. Component-level rules

### 4.1 Status vocabulary and badge tones

One vocabulary, reused everywhere a state is shown — today Overview says
`"Degraded:"`, `ErrorBox` says `"Error:"/"Session required:"/"Forbidden:"/
"Unavailable:"`, and severities only distinguish two of four values. Fix:

| Concept | Values | Tone | Where |
|---|---|---|---|
| Operational mode | `MARKET_DATA` `RECORD` `REPLAY` `BACKTEST` `PAPER` `SHADOW` | `PAPER`=ok(green), `RECORD`=accent(blue), `REPLAY`/`BACKTEST`=warn(amber, "not live data"), `MARKET_DATA`/`SHADOW`=dim | mode banner, everywhere |
| Alert/incident severity | `INFO` `WARNING` `HIGH` `CRITICAL` | `INFO`=dim, `WARNING`=warn, `HIGH`=`--high` (new, currently unused token), `CRITICAL`=bad | `severityTone()` in `ui.tsx`, extend to 4 branches |
| Alert lifecycle | `active` `acked` `resolved` | `active`=warn, `acked`=dim, `resolved`=ok | Alerts page (unchanged, already correct) |
| Circuit breaker | `OPEN` `HALF_OPEN` `CLOSED` | `OPEN`=bad, `HALF_OPEN`=warn, `CLOSED`=ok | Risk Center (unchanged, already correct) |
| Order book / feed health | `HEALTHY` `SYNCING` `STALE` `DISCONNECTED`/`CORRUPTED` | `HEALTHY`=ok, `SYNCING`/`STALE`=warn, `DISCONNECTED`/`CORRUPTED`=bad | Exchanges (unchanged) |
| Campaign run status | `queued` `running` `done` `failed` | `queued`=dim, `running`=warn (in progress, not yet a verdict), `done`=ok, `failed`=bad | Campaigns (unchanged) |
| Campaign **verdict** | backend-supplied severity, not string-matched | worst flag's severity wins the row tone | `[BL-05a, BL-05b]` |
| API error prefix | one function, `ErrorBox`, used everywhere including Overview/System | `401`→"Session required:", `403`→"Forbidden:", `404`→"Unavailable:", other→"Error:" | Remove the ad hoc `"Degraded:"` string; Overview/System route through `Await`/`ErrorBox` like every other page |

### 4.2 Empty / loading / error states

Rule: **an empty state names the console action that fills it, or the
precise reason it's empty in operator language; it never names an
environment variable or a shell command.** Where the real fix requires a
deploy-time change the operator cannot make from this session, say so
plainly and name the doc, not the variable — the operator can still act on
"see docs/deployment.md," they can't act on "requires
`ARB_DATABASE_URL`" from inside a browser tab.

Rewrites:

| Current | Replacement |
|---|---|
| `paper/page.tsx:80` "persisted cycles (requires ARB_DATABASE_URL)" | "No persisted cycles yet. Persistence needs a database connection — see docs/deployment.md if this deployment doesn't have one configured." |
| `reports/page.tsx:39` "requires ARB_DATABASE_URL" | Same pattern: "No reports yet. Generate one above, or persistence isn't configured for this deployment — see docs/deployment.md." |
| `audit/page.tsx:29` "requires ARB_DATABASE_URL" | "No audit events recorded yet for this filter." (audit only appearing empty because DB is absent is already covered by the system-degraded banner, §4.1 — don't repeat the variable name here too) |
| `replay/page.tsx:85` "run arbd with ARB_MODE=RECORD to capture" | "No recordings yet. Start one from **Campaigns → Recorder**." (link to the actual page that does this — recording is already console-driven; this copy predates that) |
| `replay/page.tsx:95-97` "Replay command" column (`ARB_MODE=REPLAY ...`) | Replace with a **"Run replay"** button once the backend worker exists (`[BL-17]`); until then, relabel the column "Replay (CLI only for now)" and keep the command as reference text, not the primary affordance — don't remove the honesty, remove the pretense that it's a console action |
| `paper/page.tsx:70` "Paper engine not running (mode is not PAPER)." | "Paper engine is not running — current mode is **{mode}**. Paper trading requires PAPER mode; this deployment is running {mode} instead." (name the actual current mode, don't just say what it isn't) |

General loading rule (already followed correctly by `Loading`/`Await`):
every async section shows `"Loading {what}…"`, never a bare spinner with no
label — keep this pattern for every new page.

General error rule: every error state that can plausibly be transient
(network blip, WS reconnect) should say so — e.g. Campaigns already does
this well with its WS status line (`"WS connected/connecting/closed"`
visible on the page at all times); replicate that visible-connection-state
pattern on every page with a live WS subscription (currently only Scanner
and Campaigns have it; Overview's future live status strip should too).

### 4.3 Confirmations for destructive/expensive actions

No confirmation primitive exists today (`[BL-02]`). Build one shared
component, `ConfirmDialog`, in `ui.tsx`:

- Modal (focus-trapped, `Escape` cancels, click-outside cancels for
  non-destructive confirms but **not** for destructive ones — require an
  explicit Cancel/confirm click so a stray click outside can't be mistaken
  for either choice on a paper reset).
- Props: `title`, `body` (ReactNode — supports the diff tables in §3.3),
  `confirmLabel`, `cancelLabel`, `danger?: boolean` (red confirm button for
  destructive actions), `onConfirm`, `onCancel`.
- Applies to: Strategy config apply/rollback (§3.3), AI recommendation
  approve (§3.3), Paper Reset (§5 BL-10, once it exists) — reset is the
  clearest "destructive" case: type-to-confirm the literal word `RESET` for
  that one action specifically, mirroring how the platform already treats
  paper resets as uniquely dangerous per SKILL §37's "reset ONLY with
  strong confirmation" (stronger than a plain Cancel/Confirm dialog).
- Does **not** apply to: Pause/Resume (reversible, one click each, already
  correct), Alert Ack (reversible), Alert Resolve for non-critical
  (reversible via the "still show resolved" guarantee), recorder Start/Stop
  (already has adequate button-state feedback, and stopping cannot lose
  data per deployment.md's segment-close guarantee).

### 4.4 Form validation messages

Pattern established by `params.go`'s `Validate()` — reuse its exact bound
language, translated to plain units, field-by-field instead of one
page-level string:

- `scanner.ttl_ms out of [50,10000]` → *"Opportunity TTL must be between 50
  and 10,000 ms."*
- `risk.max_capital_utilization out of (0,1]` → *"Max capital utilization
  must be greater than 0% and at most 100%."*
- `risk.max_drawdown out of (0,1)` → *"Max drawdown must be between 0% and
  100% (exclusive)."*
- `notifications.cooldown_seconds out of [1,3600]` → *"Alert cooldown must
  be between 1 second and 1 hour."*
- Unknown severity/channel in routes → *"'{value}' isn't a recognized
  {severity/channel} — use INFO/WARNING/CRITICAL and web/telegram."*

Validate client-side against the same bounds for instant feedback, but
always trust and surface the backend's response as authoritative (the
backend is the source of truth for every financial/risk calculation per
SKILL §76 — this extends to validation bounds; the frontend copy must not
drift from `params.go` if bounds change server-side, so client-side checks
should be treated as UX sugar, and any backend rejection always wins and
replaces the optimistic client message).

### 4.5 Table density

Current `Table` component (`ui.tsx:68-96`) is appropriately dense
(`text-[13px]`, `py-1.5` rows, no zebra striping needed given the border
row separator) — keep this as the standard for all new tables (Orders,
Fills, Users, PnL breakdowns). Two additions needed as tables grow:
- **Sticky header** on tables likely to scroll past one screen (Opportunity
  history, Audit log, Orders/Fills) — currently none scroll independently,
  the whole page scrolls.
- **Row virtualization** once any table can exceed ~500 rows client-side —
  not needed yet at current data volumes (paper cycles capped at `limit`
  params throughout the client), but Orders/Fills (§5, BL-20) should be
  built with it from the start since a single-operator paper session can
  accumulate thousands of fills over weeks. `[BL-22]`
- **Exception to the codebase's default long-text pattern.** The prevailing
  idiom for a long cell is `max-w-* truncate` + `title=` (used today in
  `reports/page.tsx:44`, `alerts/page.tsx:59`, `ai/page.tsx:53` — all
  reasonable there, since those are secondary/explanatory text). The
  Campaigns Verdict column (§3.2) is an explicit exception: verdict text
  always renders in full, wrapped, never truncated or parked in a tooltip,
  because a hidden verdict is functionally a hidden verdict.

### 4.6 Keyboard / accessibility

- All current interactive elements are native `<button>`/`<select>`/
  `<input>` — good baseline (no custom-div-as-button anti-pattern found).
- Missing: visible focus rings are inherited from Tailwind defaults but
  never verified against the dark theme's `--accent` (#4f8cff) for
  contrast — audit against WCAG AA on `--bg`/`--bg-panel`.
- Table rows are not keyboard-navigable as a unit (no roving tabindex) —
  acceptable today since rows contain at most one action button each
  (already reachable via Tab), but the future Triangle/Opportunity detail
  drill-down (§5, BL-26/27) should make whole rows focusable/Enter-
  activatable, not just the action button.
- Status conveyed by color must also carry text/icon — already true
  everywhere via `Badge`'s text content, keep enforcing this as new
  components are added (this is also a client-area.md-adjacent SKILL
  principle, §"accessibility: keyboard navigable tables/forms; status
  conveyed by text/icon, not color alone").
- Live regions: WS-driven updates (Scanner's live stream, Campaigns'
  recorder/run status, the future Overview status strip) should mark their
  containers `aria-live="polite"` (`aria-live="assertive"` only for new
  CRITICAL alerts) so a screen-reader user isn't silently missing state
  changes. Currently none are marked. `[BL-23]`

### 4.7 Responsive layout for narrow screens

`ConsoleShell.tsx:28-29` is a fixed `flex` with a non-collapsing
`w-56 shrink-0` sidebar — on a phone-width viewport this either overflows
or crushes the content pane; there is no breakpoint, no hamburger toggle,
and no `<meta name="viewport">` override needed but no responsive class
present at all. Per this spec's own principle ("mobile: read-and-acknowledge
works; dense editing can remain desktop-first"), the fix is not full mobile
parity but:
- Below `md`: sidebar collapses to a top bar with a hamburger revealing the
  nav as a full-height overlay (standard pattern, closes on nav or
  outside-tap).
- Overview, Alerts, and Campaigns' verdict/status views should reflow to
  single-column stat grids (`grid-cols-2` already does this reasonably;
  verify at 375px width) — these are the "read and acknowledge" surfaces an
  operator most plausibly checks from a phone (a CRITICAL alert fired, is
  the recorder still running).
- Editing surfaces (Strategy config form, Users & roles, Venues & fees) can
  stay desktop-first and show a plain notice on narrow viewports: *"Editing
  strategy configuration is easier on a wider screen — you can still view
  the active version below."* rather than attempting a cramped mobile form.

`[BL-24]`

---

## 5. Prioritized backlog

Effort: **S** = under a day, frontend-only or a tiny endpoint. **M** =
several days, one layer (frontend or backend) or a small coordinated slice
of both. **L** = new domain/table/endpoint plus UI, multi-day.

### P0 — binding-requirement violations / blocks the operator's stated ask

| ID | Item | Effort | Backend needed |
|---|---|---|---|
| BL-01 | Persistent mode banner (layout-level) + rebuilt Overview dashboard (status strip, today/current stats, exchange health, recent verdicts, quick actions) | M | None — `api.system.status()`, `api.scanner.status()`, `api.paper.cycles()`, `api.pnl()`, `api.risk()`, `api.system.health()`, `api.campaigns.list()` all exist |
| BL-02 | Shared `ConfirmDialog` component + wire to Strategy apply/rollback and AI approve (before/after diff body per §3.3) | M | None — uses existing `ConfigVersion.diff` / `config.apply`/`rollback`/`ai.approve` |
| BL-03 | Render actual before/after values from `ConfigVersion.diff` instead of just changed-path keys | S | None — `diff` field already returned, just unused in `strategies/page.tsx:114` |
| BL-04 | Extend `severityTone()` to 4 branches (add HIGH using the already-defined but unused `--high` token) and use it everywhere severity renders | S | None |
| BL-05a | Campaigns: Verdict column on Runs table (never truncated, per §4.5), fix `flagsTone()`'s `undefined` → `"ok"` default (should be `"dim"` — no verdict computed is not "verdict good"), relabel "Run campaign" → "Configure campaign…" to match its actual scroll-and-prefill behavior | S | None — frontend-only, ships independently of BL-05b |
| BL-05b | Campaign verdict severity emitted by the backend instead of frontend `BAD_PHRASES` string-matching | M | **Small backend addition**: attach a severity/tone alongside each flag string in `CampaignRun.flags` (or a parallel `flags_severity` map) so the frontend stops pattern-matching report prose |
| BL-06 | Add the single missing string `"reports:generate"` to `auth.tsx`'s OPERATOR permission set (rest of `can()` is already correct — ADMIN short-circuits to `true` at `auth.tsx:94`, so no ADMIN-only permission is actually drifted); gate Reports' "Generate daily/weekly now" buttons with `can(role, "reports:generate")` | S | None — mirror the existing backend matrix exactly for this one permission; ideally generate the whole map from the backend matrix at build time to prevent future drift |
| BL-07 | Add the 7 missing SKILL §31 nav items as disabled placeholders (Orders, Fills, Balances→merge into Portfolio rename, PnL & Analytics, Markets, Telegram, Users & Security) using the already-built disabled-entry rendering path | S | None for placeholders; see BL-11/15/19/20/21/26/27 for the pages themselves |

### P1 — operator explicitly asked for this and cannot do it from the UI today

| ID | Item | Effort | Backend needed |
|---|---|---|---|
| BL-10 | Paper Reset: button (type-to-confirm `RESET`, ADMIN-only) + endpoint | M | **Missing.** `PermPaperReset` already defined and granted to ADMIN (`rbac.go:17,51`); needs `POST /api/v1/paper/reset` route + handler; must preserve historical sessions per SKILL §37 ("do not delete historical metrics when a new session begins") |
| BL-11 | Users & roles page: list/invite/disable/change-role + backend user CRUD API | L | **Missing route, storage exists.** `internal/storage/authstore.go:42` `UpsertUser` already implements create/update; needs `GET/POST /api/v1/users`, `POST /api/v1/users/{id}/disable`, `POST /api/v1/users/{id}/role`; `PermUserManage` already defined (`rbac.go:28`, ADMIN-only) |
| BL-12 | Settings restructure into 5 sections (Markets & assets, Venues & fees, Users & roles, Strategy & risk, Notifications) per §2.2 | M | None beyond what BL-11/13/14/15 individually need — this is the assembly/IA work |
| BL-13 | Markets & assets: **read-only** panel (symbols, starting assets, triangle count) | S | None — `api.scanner.status()` already returns `markets`/`triangles` |
| BL-13b | Markets & assets: **editable** symbols/starting-assets from the UI | L | **Missing, architecturally nontrivial.** `Symbols`/`StartingAssets` live in `internal/config.go:58-59,92-93` as boot-time env, not in `strategy.Params` (hot-swappable). Needs: a new versioned config domain (or promotion into `strategy.Params`) + defined semantics for what happens live — WS resubscribe, instrument-rule reload, triangle re-enumeration — since `params.go:40`'s own comment already flags `Workers` as "applies at component start, not on hot swap," and a symbol change is strictly more disruptive. The UI must not imply a change is instant if the engine actually needs a restart or resubscribe cycle. Spec the exact behavior (live resubscribe vs. "restart required" banner) before building the form. |
| BL-14 | Structured Strategy/Risk/Notifications form (field-level inputs + live client-side bounds mirroring `params.go`'s `Validate()`) replacing the raw JSON textarea. Must: (a) render `risk.*` fields read-only for OPERATOR with an inline "requires ADMIN" note instead of a hidden 403-on-submit (`PermissionForConfigSection`, `rbac.go:72-77`); (b) tag every field `immediate` or `on restart` and carry that tag into the BL-02 confirm dialog — every field hot-swaps except `scanner.workers` (`params.go:40`) | M | None — `params.go` already defines every field, every bound, and the section→permission mapping; pure frontend form-building work |
| BL-15 | Venues & fees settings page (enabled/paper-enabled/markets/starting-assets/fee-tier/limits per exchange, SKILL §42) | L | **Missing entirely.** `PermExchangeConfig` defined (`rbac.go:27`, ADMIN-only) but zero routes exist; needs a per-exchange config domain, storage, and API before any UI is meaningful — ship the read-only Exchanges page as the interim (already exists) and mark this section "not yet editable" per §4.2's honesty rule rather than building a form against nothing |
| BL-16 | Rewrite the 4 env-var-leaking empty states (Paper, Reports, Audit, Replay) per the §4.2 table | S | None |
| BL-17 | Console-driven replay runs (kill the `./arbd` shell-command column); de-duplicate "Recorded sessions" between Replay and Campaigns pages, cross-link instead of repeating | L | **Missing.** Replay currently only compares config versions and lists recordings for reference; needs the backtest worker the page's own copy already promises ("arrives with the backtest worker") plus a `POST /api/v1/replays` (or similar) endpoint |
| BL-18 | System Health: full payload (CPU, memory, goroutines, GC, DB pool, queue depths, msgs/sec, per-exchange latency/reconnects/seq-errors) + move page onto `usePoll` | L | **Missing** (page's own comment cites tracked work "T-035"); needs the backend to actually collect and expose these metrics via `/api/v1/system/health` or a new endpoint |
| BL-19 | Dedicated PnL & Analytics page: breakdowns by exchange/triangle/starting-asset/market/hour/config-version + required charts (cumulative P&L, drawdown, edge/slippage/latency distributions) with sample sizes always shown | L | **Partially missing.** `api.pnl()` today only returns per-starting-asset totals (`PnLView.assets`); needs new aggregation endpoints for the other breakdowns and time-series data for charts |
| BL-20 | Dedicated Orders and Fills pages (global, filterable, cross-linkable fill→order→triangle→opportunity per SKILL §38-39) | L | **Partially missing.** `api.paper.orders(cycleID)` exists but is scoped to one cycle; needs global `GET /api/v1/orders`/`GET /api/v1/fills` with filtering, plus a fills concept that doesn't yet have client types at all |
| BL-32 | Reports: formatted detail view (executive summary / sections rendered, not raw JSON) + CSV download + structured JSON download + print-friendly view (SKILL §48) | M | **Partially missing.** JSON download is a frontend-only `Blob` export of data already on the page; CSV export and a dedicated print stylesheet need the backend to expose report sections in a tabular-exportable shape (or the frontend flattens the existing JSON, which is workable for a single-operator scope) |

### P2 — SKILL-listed console surface, not raised by the operator, nothing currently misleading

| ID | Item | Effort | Backend needed |
|---|---|---|---|
| BL-21 | Telegram status page (bot connectivity, linked chat IDs, recent pushes) | L | **Missing entirely** — no telegram routes found under `internal/api` |
| BL-22 | Row virtualization for tables that can exceed ~500 rows (Orders/Fills once built, Opportunity history at high `limit`) | S | None |
| BL-23 | `aria-live` regions on WS-driven containers; audit focus-ring contrast on dark theme; whole-row keyboard activation once detail pages exist | S | None |
| BL-24 | Two-part: (a) light-mode token set + `@media (prefers-color-scheme: light)` override for `globals.css` (currently hardcodes `color-scheme: dark` with no light tokens at all); (b) responsive collapse — hamburger sidebar below `md`, single-column stat grids verified at 375px, "wider screen" notice on dense editing surfaces | M | None |
| BL-25 | Move Overview and System Health onto the shared kit (`usePoll`/`Await`/`Stat`/`ErrorBox`) instead of duplicated/ad hoc state handling; unify error-prefix vocabulary | S | None |
| BL-26 | Triangle detail page: visual graph, per-leg VWAP/depth/fee waterfall, related order-book depth, recent performance, parameter history, AI analysis (SKILL §34) | L | **Missing entirely** — Triangles page only has the list + quality score; no per-triangle detail endpoint |
| BL-27 | Opportunity detail/reason drill-down: why detected/qualified/rejected, book versions used, calculation, risk decision, simulation result (SKILL §36) | L | **Missing entirely** — `OpportunityRow` has a `reason_code` string only, no structured explanation payload |
| BL-28 | MFA (TOTP) enrollment | L | Already tracked as MASTER_PLAN T-052 per `settings/page.tsx:47` — no new finding, just cross-referenced here for completeness |
| BL-30 | Scanner: saved views, additional filters, pause-display, pin-triangle, export (SKILL §33) | M | **Partially missing** — filtering/sort would be client-side over already-fetched data; saved views need per-user storage, which doesn't exist without BL-11's user records |
| BL-31 | Risk Center: persisted risk-event timeline (not just the session-scoped rejection counter that resets on restart) | M | **Missing** — `api.risk().reject_reason_counts` is in-memory only; needs a persisted `risk_events` read model |
| BL-33 | Alerts: visual provenance for auto-resolved vs. operator-resolved; optional browser notification for new CRITICAL alerts while backgrounded | S | None — `Alert.resolved_by` already exists on the type; just needs to be rendered |
| BL-34 | Audit Log: hide or annotate the nav item for VIEWER (who lacks `PermViewAudit`) instead of letting them reach a page that 403s | S | None |

---

## 6. What's already right (do not regress)

- Every financial value crosses the wire as a string and is never
  recomputed client-side (`client.ts:1-3` states this as policy and every
  page observed honors it).
- The WS hub (`ws.ts`) implements proper snapshot/seq/resync semantics and
  every WS-consuming page (Scanner, Campaigns) shows its own connection
  status inline — extend this pattern, don't replace it.
- RBAC is correctly backend-enforced everywhere checked
  (`s.requirePerm`/`s.requireCSRF` on every mutating route) — the frontend
  `can()` drift (BL-06) is a display bug, not a security hole, and should
  stay framed that way.
- The campaign Verdict panel's core promise — flags rendered verbatim,
  never softened or truncated — is exactly right and must be the template
  for every future "tell the operator the honest number" surface (PnL,
  AI recommendations, risk rejections).
- Paper-only framing is consistent and prominent everywhere checked (login
  page, sidebar subtitle, Settings' security posture, Campaigns' explainer
  paragraph) — the mode banner (BL-01) makes this dynamic and universal
  rather than removing the existing static reminders.
