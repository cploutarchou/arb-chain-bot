# Client-area refinement — UX specification

Status: SPEC 2026-09-12. Author: UX design (design only; no application code
in this change). Scope: `web/` console shell, information architecture,
page-level layout regions, filter semantics, table columns, state rules,
Settings categories, keyboard/mobile/accessibility contracts.

Primary evidence: `docs/design/client-area-audit-2026-09-12/README.md` and
its seven numbered captures (desktop dark mode, authenticated ADMIN session,
localhost:3000, 2026-09-12). Secondary evidence: `docs/audit/ui-ux-audit.md`
(findings F1–F20), the live `GROUPS` array in
`web/src/components/ConsoleShell.tsx`, `internal/auth/rbac.go`, and the
prior specs listed in §1.

The user question this document is organised around, in order:

1. Is the platform connected, and is paper simulation running or paused?
2. What needs my attention, and what can I safely do next?
3. Where do I find candidates, understand one, and inspect a simulation?
4. What are my simulated results, costs, and evidence limitations?
5. Where do I change my own preferences versus administer the platform?

Everything in this document is our own design and copy. No competitor
copy, icon set, layout or screenshot is reproduced anywhere in it.

---

## 1. Supersession note

### 1.1 Superseded requirements

| Prior requirement | Source | Status | Replaced by |
|---|---|---|---|
| "Keep the flat sidebar … reorganize into five visually grouped sections"; the five-group ASCII sidebar | `console-ux-audit.md` §2 | **Superseded** | §3 six primary destinations with contextual sub-navigation. Evidence: README §1 — 29 entries across six groups plus three pinned entries in one always-expanded column. |
| Two-density shell: icon rail (one icon per *group*) beside an independently scrolling label column | `docs/design/ux/console-v2.md` §2.1 | **Superseded** | §2 single primary nav column, icon **and** label on the same row. README §1 names the separate group icon rail alongside an independently scrolling label column as a hierarchy defect; with six destinations the rail carries no information the labels do not. The "collapse sidebar to icon-only" toggle in §2.1 is also dropped — an icon-only nav with six destinations saves ~170px and costs every label. |
| Seven-group nav table (Operate, Scanner Suite, Portfolio, Research, Control, System, pinned Settings) | `console-v2.md` §2.2 | **Superseded** | §3 + §4 route map. The client/operator split in §2.2's prose is preserved as a *visibility* rule (§3.3), not as two different group lists. |
| Settings as five sections (Markets & assets, Venues & fees, Users & roles, Strategy & risk, Notifications) | `console-ux-audit.md` §2.2 | **Superseded** | §9 three *in-page* categories by audience (Account, Notifications, Administration) — two for an ordinary member. Organisation and Billing are separate routes (`/org`, `/billing`) and are reached from the sidebar, not as tabs; an earlier draft of this row listed them as categories, which the page never built. The five old sections all survive as panels inside Administration, with every anchor preserved (§9.2). |
| Overview region order: status strip → Today counters → Current → exchange health → recent verdicts → quick actions | `console-ux-audit.md` §2.1 | **Superseded** | §5.1 status → attention → simulation summary → next actions → collapsed diagnostics. README §1: twelve status cards then six session-counter cards then further current-state and exchange-health cards, with operational counters given the same prominence as user outcomes. |
| Screener stats strip of five cells plus two exclusion counters, and the ten-column default table | `console-v2.md` §6.2 | **Superseded** | §8.2 four default strip cells, seven default columns, the rest behind a column picker; exclusion counts move into the Advanced filter header. README §2: seven counters and a fully expanded filter form take up much of the first viewport, and the freshness/action columns fall outside the initial view. |
| Empty-state rewrite row: `paper/page.tsx` "No persisted cycles yet. Persistence needs a database connection — see docs/deployment.md …" | `console-ux-audit.md` §4.2 table | **Superseded (this row only)** | §7.4 branches on the persistence signal the console already polls. README §5: the history empty message discusses database configuration even though Overview showed the database connected. The §4.2 *rule* — an empty state names the console action or the precise reason, never an env var or a shell command — stays in force unchanged. |
| Calculator result panel ordering "always net-first" with Liquidity OK as a badge beside the numbers | `console-v2.md` §6.5 | **Superseded** | §8.3: the backend feasibility outcome is its own region *above* the estimate. README §4: green net/net-bps were displayed while a separate lower card said liquidity was insufficient. Net-before-gross inside the estimate is unchanged. |
| Auto-Paper page identified by rule row alone, `Rule` column as the first cell | `console-v2.md` §6.7 | **Extended, not replaced** | §8.5 adds a rule display name, a visible verbatim identifier, and a "Configure this rule" action. README §6: the main identifier is a long rule ID with no prominent next step. |

### 1.2 Safety and honesty rules preserved unchanged

These are **not** superseded. Any implementation that weakens one is a
defect, regardless of anything else in this document.

| Preserved rule | Source |
|---|---|
| Status vocabulary and badge tones (mode, severity, alert lifecycle, breaker, book/feed health, campaign status, campaign verdict, the four API error prefixes) | `console-ux-audit.md` §4.1 |
| Empty states name an action or a precise reason, never an env var or a shell command; `"Loading {what}…"` never a bare spinner; a request failure is never rendered as "no data" | `console-ux-audit.md` §4.2, `AGENTS.md` "Console" |
| Confirmation scoping: type-to-confirm `RESET` for paper reset (ADMIN-only, paused-only), type-to-confirm `RESTART` for engine restart, diff-confirm for versioned config; no confirmation on reversible pause/resume/ack | `console-ux-audit.md` §4.3, observed in `PaperControl.tsx`, `ConsoleShell.tsx` `RestartBanner` |
| Field-level validation copy mirroring `params.go` bounds, with the backend's rejection always authoritative | `console-ux-audit.md` §4.4 |
| Dense table standard (`text-[13px]`, `py-1.5`), sticky headers on long tables, virtualisation above ~500 rows, campaign verdict text never truncated | `console-ux-audit.md` §4.5 |
| Data-age canon: ≤1× poll plain, >1×–3× warn, >3× literal `STALE {age}` plus a faint row dim; text is the carrier, colour is reinforcement | `console-v2.md` §3 |
| Contracted literals, never reworded: `STALE {age}`, `no-transfer, top-of-book`, `unknown (venue requires API key)` | `console-v2.md` §3, `scanner-suite.md` §7 |
| Row detail as a non-modal right drawer for Screener/Perpetuals; dedicated `[id]` pages for Triangle/Opportunity/Cycle | `console-v2.md` §4 |
| Three distinct "can't touch this" states — unbuilt / role-restricted / entitlement-gated — never one grey span; gating is display-only, the backend is the gate | `console-v2.md` §2.4 |
| Notification centre marks items *seen*, never *acknowledged*; CRITICAL alerts never disappear and Alerts stays the record | `console-v2.md` §2.5 |
| Sample size is always its own visible column beside any rate or percentage; `n = 0` rows render rather than vanish; skip reasons render with their reason | `console-v2.md` §6.7, §10.3, §10.8 |
| Numbers-first copy: net of fees leads, gross is subordinate; nothing is described as guaranteed or risk-free; every published aggregate cites `docs/campaigns/` | `console-v2.md` §10, `AGENTS.md` |
| Money crosses the wire as decimal strings and is rendered verbatim; the console never recomputes a financial value | `console-ux-audit.md` §6, `AGENTS.md` |
| Verdicts, risk outcomes and backend error messages render verbatim, never softened | `console-feature/SKILL.md` §3 |
| Settings write flow: edit → preview → `DiffTable` in `ConfirmDialog` → apply with `parent_version` → versions/rollback, backend `field_timing` chips, 409 `stale_version` handled | `console-feature/SKILL.md` §3, `console-ux-audit.md` §3.3 |
| Secrets vault is write-only; no exchange trading credential field exists anywhere in the console, masked or not | `AGENTS.md`, `console-v2.md` §1 |
| Accessibility floor: keyboard-navigable tables and forms, status conveyed by text/icon not colour alone, `aria-live` on live regions, focus-ring contrast audited in both themes | `console-ux-audit.md` §4.6, `console-v2.md` §9 |
| Mobile principle: read-and-acknowledge works at phone width; dense editing stays desktop-first with a plain notice | `console-ux-audit.md` §4.7, `console-v2.md` §8 |
| Open questions still open: `screener_settings` field timing; package→limit table; regime tags on cycles; upgrade route target | `console-v2.md` §13 (1–4) |

### 1.3 Unresolved prior findings this document depends on

These are existing tracked defects; the layouts below assume the fix, and
must not ship a regression around them.

- `ui-ux-audit.md` **F8** — a float divide on a persisted rule threshold in
  onboarding. §6.3 forbids float arithmetic in every display path specified
  here; F8 itself is the tracked fix.
- **F17** — `/settings#…` anchors land before the target section has
  height. §9.2 specifies the required scroll-after-load behaviour.
- **F15** — filter chips without `aria-pressed`. §11.3 requires the shared
  chip semantics for every filter control specified here.
- **F19** — row actions in the last column of a horizontally scrolling
  table. §8.2 moves the detail action to a row-level activation.

---

## 2. Shell layout regions

One nav column. No second icon rail. Regions, top to bottom:

| Region | ID | Present at | Contents |
|---|---|---|---|
| Top bar | `shell.topbar` | all widths | Brand; command palette trigger (§11.5); notification bell (unchanged, `console-v2.md` §2.5); theme toggle; account menu (user, role badge, Settings, Sign out) |
| Mode strip | `shell.mode` | all widths, full width, directly under the top bar | Mode indicator (§2.1) on the left; scoped paper control (§2.2) on the right; the existing `RestartBanner` occupies a second line inside this region when restart state is not `ready` |
| Primary nav | `shell.nav` | ≥ `md`: fixed left column 208–224px. < `md`: hamburger overlay | Six destinations in §3.1 order, then a rule, then `operator-admin`, then the two footer-pinned shortcuts **Organisation** and **Billing**. One row per destination: our own glyph + text label, never icon-only. The active destination's contextual surfaces render as an indented list beneath it (route-driven, not click-driven) |
| Sub-nav | `page.subnav` | all widths | Contextual surfaces of the active destination, as a horizontal strip of links under the page `h1`. Duplicates the sidebar's indented list on purpose: the sidebar answers "where am I", the strip answers "what else is here" |
| Page header | `page.header` | all widths | One `h1` (the destination or surface name), a one-line purpose sentence, and the page's own updated-at/connection state |
| Main | `page.main` | all widths | The page's regions (§5–§8) |

Rules:

- The nav column scrolls with the page, not independently. README §1 names
  the independently scrolling label column beside the icon rail as part of
  the hierarchy problem; with six destinations the list no longer needs its
  own scroll container at any supported width.
- Group collapse persistence (`arb.nav.collapsed-groups`) is retired for
  destinations. Destination expansion is derived from the active route, so
  the active surface's siblings are always visible and nothing is hidden by
  stale `localStorage`. The key is left unread rather than migrated.
- `shell.mode` is chrome, never nav. It does not scroll away on desktop
  (position sticky) and is the second row of the mobile top bar, as today.
- **Exactly one Settings row exists.** Settings is the sixth destination
  (§3.1) and is not additionally footer-pinned. Only Organisation and
  Billing keep footer-pinned shortcuts, which is what preserves their
  current 1-activation reachability in §4. `/settings` is also reachable
  from the account menu in `shell.topbar`; that is a menu item, not a
  second nav row.

### 2.1 Persistent paper-only mode indicator

Unchanged in logic from `ConsoleShell.tsx` `ModeBanner`; restated because
it is a non-negotiable:

- Always rendered as visible text, never colour alone, never only a
  `title`. Full sentence at ≥ `md` (`PAPER TRADING ONLY — live execution
  permanently disabled`), dot + mode word at < `md`, with the full sentence
  as the accessible name.
- Running mode is authoritative; a differing configured mode annotates it
  (`configured: {mode} — restart pending`) rather than replacing it.
- Degrades to `Mode unknown (backend unreachable)` — never to a blank or a
  reassuring default.
- `MODE_COPY` tones unchanged (`console-ux-audit.md` §4.1).

### 2.2 Clearly scoped pause / resume

The shell control is the triangular paper engine's pause, driven by
`api.scanner.status().paper` and `api.paper.pause()/resume()`. It must name
its scope, because the platform has a second simulation path (rule-driven
auto-paper) whose relationship to this call is not established.

| Element | Copy |
|---|---|
| State label | `Paper engine (triangular): RUNNING` / `: PAUSED` |
| Button (running) | `Pause triangular paper engine` |
| Button (paused) | `Resume triangular paper engine` |
| Confirm body (pause) | `In-flight simulations settle to their own outcome — nothing already filled is cancelled. No new triangular cycle starts until you resume.` |
| Compact (< `md`) | `TRI: RUNNING` chip + icon button whose accessible name is the full button copy above |

- Pause confirms once; resume does not (reversible, nothing put at risk).
  Unchanged from `PaperControl.tsx`.
- Every outcome is a toast naming the backend's own message and, on
  failure, the HTTP status. Unchanged.
- VIEWER sees the state text and no button. Unchanged; the backend enforces
  `paper:control` regardless.
- **The dialog must not make any claim about rule-driven auto-paper.**
  Whether `POST /api/v1/paper/pause` also halts auto-paper executions is
  **requires backend confirmation** (§13.1). Until confirmed, rule
  simulations are controlled per rule on Alerts & Rules, and the Rule
  simulations sub-view states that in one line. README §6 explicitly warns
  against implying a global pause scope without confirming semantics.

---

## 3. The six primary destinations

### 3.1 Destinations, IDs and landing routes

| Nav ID | Visible label | Purpose line (shown under the `h1`) | Landing route |
|---|---|---|---|
| `overview` | Overview | What the platform is doing right now, and what needs you. | `/overview` |
| `discover` | Discover | Find and understand candidate spreads and cycles. | `/screener` |
| `paper` | Paper Trading | Watch simulations run and read what they did. | `/paper` |
| `research` | Research & Results | Simulated results, costs and the evidence behind them. | `/pnl` |
| `alerts` | Alerts & Rules | Incidents that need a person, and the rules that watch for them. | `/alerts` |
| `settings` | Settings | Your account, your organisation, billing, notifications. | `/settings` |
| `operator-admin` | Platform administration | Deployment-wide operations and configuration. Not your organisation's settings. | `/system` |

`operator-admin` is listed last, visually separated by a rule, and carries
the subtitle verbatim as written above at every width. Organisation
administration (`/org`) lives in `settings`, never here.

### 3.2 Contextual surfaces per destination

Labels are explanatory first, with the historical page name in
parentheses so an existing bookmark, a support conversation and the nav all
use connectable vocabulary. This is the answer to "the user must not have
to intuit Scanner vs Screener".

| Destination | Sub-view ID | Label | Route |
|---|---|---|---|
| `discover` | `discover.spreads` | Cross-exchange spreads (Screener) | `/screener` |
| | `discover.cycles` | Triangular cycles, live (Scanner) | `/scanner` |
| | `discover.triangles` | Triangle catalogue | `/triangles` |
| | `discover.candidates` | Candidate history (Opportunities) | `/opportunities` |
| | `discover.perps` | Perpetual basis | `/perpetuals` |
| | `discover.funding` | Funding history | `/funding` |
| | `discover.calculator` | Spread calculator | `/calculator` |
| `paper` | `paper.live` | Live simulations | `/paper` |
| | `paper.rules` | Rule simulations (Auto-Paper) | `/auto-paper` |
| | `paper.balances` | Simulated balances | `/portfolio` |
| | `paper.orders` | Simulated orders | `/orders` |
| | `paper.fills` | Simulated fills | `/fills` |
| `research` | `research.pnl` | Simulated P&L and costs | `/pnl` |
| | `research.screener-evidence` | Screener paper evidence | `/screener-reports` |
| | `research.ops-reports` | Operational reports | `/reports` |
| | `research.campaigns` | Recorded-data campaigns | `/campaigns` |
| | `research.replay` | Replay and backtesting | `/replay` |
| | `research.ai` | AI advisor | `/ai` |
| `alerts` | `alerts.incidents` | Incidents | `/alerts` |
| | `alerts.rules` | Alert rules | `/scanner-alerts` |
| | `alerts.engine-params` | Engine strategy and risk parameters | `/strategies` |
| `settings` | `settings.account` | Account | `/settings` (Account category) |
| | `settings.organisation` | Organisation | `/org` |
| | `settings.billing` | Billing | `/billing` |
| | `settings.notifications` | Notifications | `/settings` Notifications category |
| | `settings.telegram` | Telegram delivery | `/telegram` |
| | `settings.administration` | Administration | `/settings` Administration category |
| | `settings.setup` | Setup wizard | `/onboarding` |
| `operator-admin` | `operator-admin.risk` | Risk and safety (Risk Center) | `/risk` |
| | `operator-admin.system` | System health | `/system` |
| | `operator-admin.exchanges` | Exchange feeds | `/exchanges` |
| | `operator-admin.audit` | Audit log | `/audit` |
| | `operator-admin.config` | Platform configuration | `/settings` Administration category |

`settings.administration` and `operator-admin.config` are the **same
surface at the same URL**, entered from two places. That satisfies both
"platform configuration goes under an explicitly labelled operator
administration area" and "existing URLs and `/settings` anchors do not
change". It is rendered once; only the breadcrumb line above it differs
(`Settings → Administration` vs `Platform administration → Configuration`).

### 3.3 Role and entitlement visibility per destination

Two independent axes exist in `Me`: the platform RBAC `role`
(`ADMIN`/`OPERATOR`/`VIEWER`, matrix in `internal/auth/rbac.go`), the
organisation `org_role` (`OWNER`/`ADMIN`/`MEMBER`/`VIEWER`), plus
`platform_admin: boolean` and the resolved `entitlements` document. They
are not interchangeable.

**Binding rule: a destination renders when the signed-in user can reach at
least one surface inside it. Every surface keeps its own existing gate,
unchanged. Grouping never adds a gate.**

| Destination | Rendered when | Notes |
|---|---|---|
| `overview` | any authenticated session | Always. |
| `discover` | `screener:view` **or** `view:opportunities` | Both are VIEWER+ today, so every current role keeps access. `discover.perps` / `discover.funding` additionally consult `venues.perps_enabled`; when false the sub-nav entry renders in the entitlement-gated state (lock glyph, package chip, clickable to `/billing`) — `console-v2.md` §2.4, unchanged. |
| `paper` | `view:portfolio` (VIEWER+) | Read for all roles. `paper:control` (OPERATOR+) shows the pause/resume button. `paper:reset` (ADMIN) shows the reset disclosure. `paper.rules` consults `auto_paper.strategies`; empty array → entitlement-gated, exactly as `ConsoleShell.tsx` does today. |
| `research` | `view:portfolio` **or** `reports:view` (VIEWER+) | `reports:generate` (OPERATOR+) shows the generate buttons; rendered read-only, never hidden-then-403. `ai:approve` shows approve/reject. Window pickers consult `history.retention_days`; export controls consult `history.export_formats` and `history.export_scheduled`. |
| `alerts` | `view:dashboard` (VIEWER+) | `alerts:ack` (OPERATOR+) shows ack/resolve. `screener:config` (ADMIN) shows rule create/edit/delete; rule counts consult `rules.max_active`, kinds consult `rules.kinds`, channels consult `alerts.channels`. `alerts.engine-params`: `scanner:config` for non-risk sections, `risk:config` (ADMIN) for `risk.*`, which renders **read-only with an inline "changing risk limits requires ADMIN" note** for OPERATOR — never a hidden control that 403s on submit (`console-ux-audit.md` BL-14a). |
| `settings` | any authenticated session | Account always. Organisation and Billing are member-readable; their mutations gate on `org_role` OWNER/ADMIN, as the pages already do. Notifications readable by all, editable with `scanner:config`. Administration is listed when the user holds any of `system:config`, `exchange:config`, `users:manage`, `risk:config`, `screener:config`. |
| `operator-admin` | `view:risk` **or** `view:system` **or** `view:audit` | All three are held by VIEWER except `view:audit` (OPERATOR+), so **every current role sees this area and keeps exactly the reachability it has today**. `operator-admin.audit` keeps the existing annotate-don't-hide treatment for VIEWER (grey entry, `Requires OPERATOR or ADMIN`). `operator-admin.config` is listed only under the Administration permissions above. |

`platform_admin` gates nothing at the *area* level. It is consulted only
where the backend already consults it, inside the Administration category.
Hiding is never the security mechanism: the backend is the gate
(`AGENTS.md`, `console-ux-audit.md` §6).

---

## 4. Complete old → new route map

**"Navigation activations from Overview" counts one click or Enter on a
navigation control that changes the route**, starting on `/overview`.
Opening a row-detail drawer is not a route change and is counted 0 with the
drawer noted. Deep links, bookmarks and the command palette (§11.5) always
reach any route in one activation; the counts below describe pure nav
traversal.

Today every one of the 29 sidebar entries is 1 activation, at the cost the
README documents in §1: 29 entries plus 3 pinned plus a redundant icon rail
plus an independently scrolling label column, with no task grouping. The
trade below keeps the six highest-frequency destinations at 1 and moves
lower-frequency surfaces to 2, where the second activation is a sibling tab
that is already on screen.

| Current route | Destination | Contextual sub-view | How it is reached | Activations from Overview |
|---|---|---|---|---|
| `/` | — | — | redirect to `/overview` (unchanged) | n/a |
| `/login` | — | — | unauthenticated entry (unchanged) | n/a |
| `/overview` | `overview` | — | primary nav; brand click | 0 (current page) |
| `/onboarding` | `settings` | `settings.setup` | Overview first-run banner; Settings → Setup wizard | **1** via the Overview banner; 2 via Settings |
| `/screener` | `discover` | `discover.spreads` | primary nav `Discover` (landing) | **1** |
| `/scanner` | `discover` | `discover.cycles` | Discover sub-nav | 2 |
| `/triangles` | `discover` | `discover.triangles` | Discover sub-nav | 2 |
| `/triangles/[id]` | `discover` | `discover.triangles` detail | row link on `/triangles`; cycle detail; opportunity detail | 3 |
| `/opportunities` | `discover` | `discover.candidates` | Discover sub-nav; "Candidate history" link on `/scanner` | 2 |
| `/opportunities/[id]` | `discover` | `discover.candidates` detail | row link on `/opportunities` | 3 |
| `/perpetuals` | `discover` | `discover.perps` | Discover sub-nav | 2 |
| `/funding` | `discover` | `discover.funding` | Discover sub-nav; "Open in Funding" in the perps drawer | 2 |
| `/calculator` | `discover` | `discover.calculator` | Discover sub-nav; "Open in calculator" from a spread drawer (drawer open = 0) | 2 |
| `/paper` | `paper` | `paper.live` | primary nav `Paper Trading` (landing) | **1** |
| `/auto-paper` | `paper` | `paper.rules` | Paper sub-nav; "Rule simulations" link from a rule row on `/scanner-alerts` | 2 |
| `/portfolio` | `paper` | `paper.balances` | Paper sub-nav; "Simulated balances" link from Overview's simulation summary | 2 (1 via the Overview summary link) |
| `/orders` | `paper` | `paper.orders` | Paper sub-nav; cycle detail link | 2 |
| `/fills` | `paper` | `paper.fills` | Paper sub-nav; `?cycle=` link from an order row | 2 |
| `/cycles/[id]` | `paper` | `paper.live` / history detail | row link in Simulation history, Fills, or an opportunity's simulation block | 2 |
| `/pnl` | `research` | `research.pnl` | primary nav `Research & Results` (landing); "See my results" on Overview | **1** |
| `/screener-reports` | `research` | `research.screener-evidence` | Research sub-nav | 2 |
| `/screener-reports/[id]` | `research` | `research.screener-evidence` detail | row link | 3 |
| `/reports` | `research` | `research.ops-reports` | Research sub-nav | 2 |
| `/campaigns` | `research` | `research.campaigns` | Research sub-nav | 2 |
| `/replay` | `research` | `research.replay` | Research sub-nav; "Recorded sessions" cross-link on `/campaigns` | 2 |
| `/ai` | `research` | `research.ai` | Research sub-nav | 2 |
| `/alerts` | `alerts` | `alerts.incidents` | primary nav `Alerts & Rules` (landing); Overview attention item; notification panel footer | **1** |
| `/scanner-alerts` | `alerts` | `alerts.rules` | Alerts sub-nav; "Configure this rule" from `/auto-paper` | 2 |
| `/strategies` | `alerts` | `alerts.engine-params` | Alerts sub-nav; "Engine parameters" link from `/risk` | 2 |
| `/settings` | `settings` | `settings.account` | primary nav `Settings` (landing); account menu in `shell.topbar` | **1** |
| `/settings#operating-mode` | `settings` | `settings.administration` | Settings → Administration; `operator-admin` → Platform configuration | 2 |
| `/settings#markets` | `settings` | `settings.administration` | as above | 2 |
| `/settings#scanner-suite` | `settings` | `settings.administration` | as above | 2 |
| `/settings#logging` | `settings` | `settings.administration` | as above | 2 |
| `/settings#ai` | `settings` | `settings.administration` | as above; "AI settings" link on `/ai` | 2 |
| `/settings#platform-versions` | `settings` | `settings.administration` | as above; `RestartBanner` rollback link | 2 (1 from the restart banner) |
| `/settings#users` | `settings` | `settings.administration` | as above | 2 |
| `/settings#notifications` | `settings` | `settings.notifications` | Settings → Notifications; both existing `/telegram` links | 2 |
| `/settings#security` | `settings` | `settings.administration` | as above | 2 |
| `/settings#session` *(new id)* | `settings` | `settings.account` | Settings landing; account menu | 1 |
| `/settings#venues` *(new id)* | `settings` | `settings.administration` | Settings → Administration | 2 |
| `/settings#strategy-risk` *(new id)* | `settings` | `settings.administration` | Settings → Administration | 2 |
| `/settings#setup` *(new id)* | `settings` | `settings.account` | Settings landing | 1 |
| `/settings#security-posture` *(new id)* | `settings` | `settings.account` | Settings landing | 1 |
| `/telegram` | `settings` | `settings.telegram` | Settings sub-nav; "Telegram delivery" from the Notifications category and from a rule's Telegram badge | 2 |
| `/org` | `settings` | `settings.organisation` | footer-pinned Organisation (unchanged); Settings sub-nav | **1** |
| `/billing` | `settings` | `settings.billing` | footer-pinned Billing (unchanged); Settings sub-nav; any entitlement-gated control | **1** |
| `/risk` | `operator-admin` | `operator-admin.risk` | Overview status "Breakers open" card and attention item; Paper Trading safety strip; `operator-admin` sub-nav | **1** from Overview and from Paper Trading; 2 via the area |
| `/system` | `operator-admin` | `operator-admin.system` | `operator-admin` nav (landing); Overview "Platform connected"/feed cards | **1** |
| `/exchanges` | `operator-admin` | `operator-admin.exchanges` | `operator-admin` sub-nav; Overview exchange-health diagnostics link | 2 |
| `/audit` | `operator-admin` | `operator-admin.audit` | `operator-admin` sub-nav (VIEWER: annotated, not navigable — unchanged) | 2 |

Routes that gain a second entry point rather than losing one:
`/risk` (Overview + Paper Trading + area), `/onboarding` (Overview banner +
Settings), `/portfolio` (Overview summary + Paper sub-nav),
`/calculator` (Discover sub-nav + every spread drawer), `/auto-paper`
(Paper sub-nav + rule rows), `/scanner-alerts` (Alerts sub-nav +
`/auto-paper` rows).

No route is rewritten, removed, aliased or redirected. Back and forward
behaviour is unchanged except that `/screener` gains filter query
parameters (§8.2), which makes Back *restore more state than it does
today*; bare `/screener` continues to resolve to the page defaults.

---

## 5. Destination specs — layout regions

Every page: one `h1`, a one-line purpose sentence, its own updated-at or
connection state, then the regions below in the order given. Every region
is an `h2` section.

### 5.1 Overview (`overview`, `/overview`)

Answers questions 1 and 2. README §1: make status, attention and next
action primary; move diagnostics into secondary sections.

| Region | ID | Contents |
|---|---|---|
| A. Status | `overview.status` | Exactly six cells, in this order: **Platform** (connected / backend unreachable), **Paper engine (triangular)** (RUNNING / PAUSED, with the scoped control inline), **Market data** (feed OK / DEGRADED + venue clock), **Safety** (breakers open `N`, links `/risk`), **Attention** (unresolved alerts `N`, links `/alerts`), **Simulation history** (recording / not stored on this deployment). Two rows of three at ≥`md`, two columns at <`md`. |
| B. What needs attention | `overview.attention` | 0–5 ranked items. Each item: severity badge (text, four values), one plain sentence, one primary action link whose text names the destination ("Review 2 unresolved alerts"). Rank order: CRITICAL alerts → breakers open → saved-but-not-running (restart pending) → degraded feed or stale venues → billing `past_due` → risk acknowledgement required → thin-evidence notice. Composed client-side from polls the console already makes; **no new endpoint** (§13.4). |
| C. My simulation so far | `overview.simulation` | Session realised P&L **per asset with the asset symbol on every figure and no truncation** (README §1: a drawdown figure is visibly truncated), fees paid, max drawdown, cycles completed / failed, capital in use vs available. Each figure carries the word "simulated" in its group label, not on every cell. Links: "Simulation history →" (`/paper`), "Simulated balances →" (`/portfolio`), "Full results →" (`/pnl`). |
| D. What I can do next | `overview.actions` | Max four task-named links, filtered by role and entitlement: "Find cross-exchange spreads →", "Watch simulations →", "Read my results →", "Review alert rules →". First-run replaces this region with the setup-wizard banner. |
| E. Engine diagnostics | `overview.diagnostics` | **Collapsed by default**, disclosure labelled `Engine diagnostics`, state persisted per user in `localStorage` (try/catch, default collapsed). Contains: session counters (opportunities detected / qualified / rejected, revalidations before execution), triangles and markets counts, exchange-health counters (frames, reconnects, REST errors, resyncs, sequence gaps), outbox written / refused / dropped counts, each with its own `Updated Ns ago`. Links to `/system` and `/exchanges`. |

Region E is where every counter the README counted in the first viewport
goes. Nothing is deleted — 435,990 detected and 399,177 rejected are real
and stay available with one activation of a disclosure, not hidden.

### 5.2 Discover (`discover`)

Sub-nav in the order listed in §3.2. Shared regions on every table
surface: stats strip (§8.2), filter card (§8.1), table, table footnote
carrying the model captions verbatim.

A persistent one-line engine distinction sits under the sub-nav, because
this is the "Scanner vs Screener" problem:

> Cross-exchange spreads compare one asset's price between two venues.
> Triangular cycles trade three pairs inside one venue. They are separate
> engines with separate data and separate results.

### 5.3 Paper Trading (`paper`, `/paper`)

README §5: put monitoring and results first, move reset below normal work,
keep its safeguards.

| Region | ID | Contents |
|---|---|---|
| A. Safety and status | `paper.safety` | Engine state, the scoped pause/resume control (§2.2, one instance — the `h1` region does not repeat the state label), current risk posture summary (breakers open, capital utilisation vs limit) with **"Risk and safety →"** linking `/risk`, and the persistence state chip. This region is why `/risk` is 1 activation from here. |
| B. Live simulations | `paper.live-table` | In-flight cycles, or the explained empty state (§7.4). Keeps the existing honest copy that the engine is running and waiting for a qualified opportunity — README §5 calls this correct. |
| C. Simulation history | `paper.history` | Persisted cycles. Columns in §8.4. Empty state branches per §7.4 — it must not mention the database when the database is healthy. |
| D. Reset this paper session | `paper.reset` | Below history, inside a collapsed disclosure labelled `Reset this paper session`, still rendered in the warning-bordered treatment. Unchanged mechanics: ADMIN only, engine must be paused, type-to-confirm `RESET`, consequences stated verbatim ("clears the running paper session and starts a fresh one; historical cycles already persisted are not deleted"). The word "Danger Zone" is replaced by the action name; the safeguard is not. |

### 5.4 Research & Results (`research`, `/pnl`)

Every surface in this destination carries a **source label** chip in
`page.header`, because the platform has several result systems that must
not be confused (`docs/user-guide/reports.md`: "two report systems … do not
confuse them").

| Sub-view | Source label chip |
|---|---|
| `research.pnl` | `Source: triangular paper engine ledger` |
| `research.screener-evidence` | `Source: screener paper evidence (nightly)` |
| `research.ops-reports` | `Source: engine operational reports` |
| `research.campaigns` | `Source: campaign runs over recorded data` |
| `research.replay` | `Source: replay over recorded data` |
| `research.ai` | `Source: AI advisor recommendations — require approval` |

Every aggregate surface carries an **Evidence limitations** block as its
last region (`research.limitations`), always rendered, never collapsed:

- Sample size `n` for each figure shown, in the same glance as the figure.
- Date window and, where the backend supplies them, regime coverage tags
  with per-regime `n` (`console-v2.md` §13 Q3 is still open — if regime
  labels are absent, the block says "regime coverage not recorded for this
  window" rather than omitting the line).
- The `docs/campaigns/…` path the aggregate was filed under. Where no
  report has been filed for the window: "No campaign report filed for this
  window yet — these figures are live aggregates, not published results."
- The verbatim footer sentence from `console-v2.md` §6.8 for any
  performance figure: *"Simulated on paper. Past paper performance is not a
  projection or guarantee of future results — see the sample size above
  before relying on it."*

### 5.5 Alerts & Rules (`alerts`, `/alerts`)

Incidents and rule configuration stay distinct surfaces with distinct
vocabulary, never merged into one table:

- `alerts.incidents` — things that happened and may need a person.
  Lifecycle `active` / `acked` / `resolved`. Ack and Resolve live here and
  only here.
- `alerts.rules` — standing configuration. State `enabled` / `disabled`. A
  rule never "resolves". Per-rule auto-paper toggle lives here, and this is
  the surface the Rule simulations view points at.
- `alerts.engine-params` — the versioned engine strategy/risk document,
  with the diff-confirm flow and the read-only `risk.*` treatment for
  OPERATOR.

Header copy under the sub-nav: *"Incidents are events. Rules are standing
instructions. Changing a rule never changes an incident's state."*

### 5.6 Settings (`settings`, `/settings`) — see §9

### 5.7 Platform administration (`operator-admin`, `/system`)

Regions on the area landing page (`/system`, unchanged route): the existing
System Health content, preceded by a one-paragraph scope statement and a
card grid of the area's surfaces with a one-line purpose each. The scope
statement is fixed copy:

> This area is deployment-wide. It affects every organisation on this
> deployment. Your own organisation's members, plan and preferences are
> under Settings.

---

## 6. Terminology

### 6.1 Implementation jargon → task language

| Implementation term (where seen) | Plain task language | Technical detail treatment |
|---|---|---|
| "persisted cycles" / `CYCLES (PERSISTED)` (README §5) | **Simulation history** | Row detail shows `cycle_id` verbatim and monospaced; the word "persisted" appears only in the diagnostics disclosure |
| in-memory ring / `IN FLIGHT (LIVE VIEW)` | **Live simulations** and **Recent activity (not stored)** | The ring's window size and capacity stay in `overview.diagnostics` and on `/system` |
| `Engine.Run` (backend string, `internal/platform/modes.go:21`) | Lead line authored by the console: **"Not available on this deployment."** | **The backend string renders verbatim, unchanged, inside an expandable "Engineering detail" line.** It is a backend-authored reason and the console must not paraphrase it (§13.6 raises the wording with the backend reviewer) |
| "backend wiring" in helper copy | "not connected yet" | — |
| `rule-01M13T0XRW1TH3H0PAFW27V5VX` (README §6) | **Rule display name**, falling back to `Unnamed rule` | The exact identifier stays visible as a monospaced secondary line with a copy button — it is needed for inspection, support and log correlation, so it is never hidden behind a hover |
| `MASTER_PLAN T-052`, `T-035`, `(separate task)` | **"Planned — not available yet."** | Task identifier retained in an expandable "Engineering reference" line on the same row |
| `Gross bps` | **Spread before fees (bps)** | Optional column; exact string in the drawer |
| `Net bps` | **Est. spread after fees (bps)** | Default column; exact string in the drawer |
| bare `Profit` (F14) | **Est. net result ({asset})** | — |
| `liquidity_unknown`, `include_unknown_liquidity` | **"Liquidity not published by this venue"** / filter label "Include lanes with unknown liquidity" | Code `LIQUIDITY_UNKNOWN` stays visible in the row's Limitations cell and in skip-reason breakdowns — it is a contracted code that appears in reports |
| `suspect`, `spread_exceeds_max_plausible`, `price_deviates_from_median` | **"Possible asset mismatch — the same ticker may not be the same asset on both venues"** | Codes stay visible in the Limitations cell and in skip-reason breakdowns; the verbatim `suspect_reason` renders in the drawer |
| `SUSPECT_MISMATCH` skip reason | **"Skipped: possible asset mismatch"** | Code retained beside the gloss |
| `reject_reason_counts` | **"Why candidates did not qualify"** | Exact reason codes stay as the row keys — they are the vocabulary of the engine's logs and reports |
| `parent_version` / HTTP 409 `stale_version` | **"Someone else changed this while you were editing."** | Both version numbers shown; the reload action is explicit |
| `field_timing` | **Effect chips: "Applies now" / "On restart"** | Chip text comes from the backend, never hardcoded (`console-feature/SKILL.md` §2.1) |
| `outbox`, `refused`, `dropped` | **"Records written / refused / dropped"** | Stays visible with counts in diagnostics — silent loss is a defect (`AGENTS.md`) |
| `Danger Zone` | **"Reset this paper session"** | The safeguards are unchanged; only the heading changes |
| `STALE {age}`, `no-transfer, top-of-book`, `unknown (venue requires API key)` | **unchanged — contracted literals** | Must not be reworded (`scanner-suite.md` §7) |

### 6.2 Two-sided rule

- **Plain language leads.** The first line a user reads on any row, card or
  empty state is task language.
- **Exact identifiers stay visible, not hovered.** Rule IDs, cycle IDs,
  session IDs, correlation IDs, settings version numbers and reason codes
  are needed for inspection and support. They render as monospaced
  secondary text with a copy affordance. F16 is explicit that
  decision-critical text must not live only in a `title`.
- **Backend strings render verbatim.** Verdicts, risk outcomes, skip
  reasons, validation errors and availability reasons are never rewritten
  by the console, even when they contain code symbols.

---

## 7. Empty, loading, error and permission states

### 7.1 The eight states, and why each is distinguishable

Every async region resolves into exactly one of these. Healthy-empty must
never be confusable with any other.

| State | ID | Visual | Explanation | Required navigation action |
|---|---|---|---|---|
| Loading | `state.loading` | existing `Loading` component, region `aria-busy="true"` | `Loading {what}…` — never a bare spinner | none |
| Healthy-empty | `state.empty` | dim body text, no border | Names the precise reason there is nothing, and includes a real count when one exists | one link to the surface that would produce data |
| Filtered-empty | `state.filtered` | dim body text | `0 of {total} {units} match these filters.` | `Clear filters` (returns to page defaults) and `Loosen a threshold` focusing the threshold input |
| Service error | `state.error` | `ErrorBox`, bordered, `role="alert"` | The backend's own message with the four-value prefix (`Session required:` / `Forbidden:` / `Unavailable:` / `Error:`) and the correlation ID when present | `Retry` plus `System health →` |
| Permission denied | `state.forbidden` | `ErrorBox` in the `Forbidden:` form | Names the minimum role: `Forbidden: reading the audit log needs OPERATOR or ADMIN.` | a link to the nearest surface the user *can* read |
| Unavailable configuration | `state.absent` | `Unavailable` component, `ABSENCE_CODES` map | `This deployment does not run {X}.` plus the doc path — never an env var, never a shell command | the doc link and one in-console alternative |
| Entitlement-gated | `state.gated` | `GatedControl` package state (lock glyph, package chip, `opacity-60`) | `Included in {Package}.` | `/billing` |
| Degraded / partial | `state.degraded` | per-cell error inside an otherwise complete region | The failing cell shows its own error; every other cell renders | the failing cell's own retry |

Rules:

- A request failure is **never** rendered as an empty state (F7,
  `AGENTS.md`).
- A fully stale table renders its stale rows, dimmed and labelled — it is
  never emptied, because a fully stale table is itself the signal
  (`console-v2.md` §7.4).
- `state.loading` never flashes into `state.empty`. A region that has never
  resolved shows `state.loading`; only a resolved-and-genuinely-empty
  response produces `state.empty`.

### 7.2 Per-surface healthy-empty copy

| Surface | Condition | Copy | Action |
|---|---|---|---|
| `discover.spreads` | filters exclude everything | `0 of {pairs_tracked} tracked pairs match these filters.` | Clear filters / Loosen threshold |
| `discover.spreads` | no venues online | `No venue is currently online, so there are no quotes to compare.` | `Exchange feeds →` |
| `discover.cycles` | engine running, nothing qualified | `No cycle qualifies right now. The engine is running and evaluating; qualified cycles appear here as they are found.` | `Why candidates did not qualify →` (`/risk`) |
| `discover.candidates` | none recorded | `No candidates recorded yet for this filter.` | widen the window |
| `discover.funding` | no base selected | `Pick a base asset above to see its funding history.` | — |
| `discover.funding` | base selected, no points | `No funding history for {base} on the selected venues and window yet. Funding accrues once per interval, so a newly enabled venue can take a full interval to show its first point.` | widen window |
| `discover.calculator` | before first submit | `Enter a size and two venues, then Calculate.` | — (never zeros that read as a result) |
| `paper.live-table` | engine running, nothing in flight | `No simulation is running right now. The engine is running and waiting for an opportunity that clears the cost and risk checks.` | `Why candidates did not qualify →` |
| `paper.live-table` | engine paused | `Paper trading is paused, so no new simulation will start. In-flight simulations, if any, are listed above.` | Resume button (role permitting) |
| `paper.live-table` | mode is not PAPER | `The paper engine is not running — this deployment's mode is {mode}. Paper trading needs PAPER mode.` | `Operating mode →` (`/settings#operating-mode`) |
| `paper.history` | see §7.4 | | |
| `paper.rules` | no rule has auto-paper on | `No rule runs automatic paper simulations yet. Turn it on for a rule to start building a record.` | `Alert rules →` |
| `paper.rules` | rule on, zero alerts | row renders with `0`, `0`, `—`, `n = 0` and the `thin` badge — never omitted | `Configure this rule →` |
| `research.*` | no data in window | `No {results} in the selected window.` plus the retention note when `history.retention_days` bounds it | widen window |
| `alerts.incidents` | none active | `No unresolved incidents. Resolved incidents stay listed below.` | show resolved toggle |
| `alerts.rules` | none created | `No alert rule yet. A rule watches for a spread, basis or carry level and tells you when it appears.` | `Create rule` (or the gated state) |
| `operator-admin.audit` | filter matches nothing | `No audit events recorded yet for this filter.` | clear filter |

### 7.3 Error and permission copy that must not regress

- Retain the single `ErrorBox` vocabulary. No page invents a prefix; the
  ad-hoc `"Degraded:"` string is not reintroduced.
- Retain `ABSENCE_CODES` for honest absence (`engine_absent`,
  `screener_absent`, and any new code) — an API profile with no engine says
  so, it does not show an empty table.
- Permission denial names the minimum role from the real matrix, never
  "restricted". Grouping in §3 does not add a gate, so a permission denial
  after this refactor means the same thing it means today.

### 7.4 "No cycles yet" must not imply a missing database

README §5 is explicit: the history empty message discussed database
configuration while Overview showed the database connected, and that text
is generic, not proof of a storage fault.

The discriminator already exists and is already polled — the console reads
`api.recordings.list().persistence: boolean`, which is exactly what
`/overview` renders as `Database: CONNECTED / NOT CONFIGURED`
(`overview/page.tsx:195`). No backend change is required for this branch.

| `persistence` | Engine state | Copy |
|---|---|---|
| `true` | running | `No simulation recorded yet. The engine is running; completed simulations are stored and appear here.` + link `Why candidates did not qualify →` |
| `true` | paused | `No simulation recorded yet, and paper trading is currently paused. Resume to let new simulations start.` + the scoped Resume button |
| `true` | mode ≠ PAPER | `No simulation recorded yet. This deployment's mode is {mode}; paper trading needs PAPER mode.` + `Operating mode →` |
| `false` | any | `This deployment does not store simulation history, so completed simulations are not kept after the session. See docs/deployment.md.` — rendered as `state.absent`, not as `state.empty` |
| poll not resolved | any | `state.loading` on this region — never the `false` copy as a default |
| the cycles request itself failed | any | `state.error` with the backend message — never any empty copy |

A database that is configured but failing is not distinguishable from a
healthy one through a boolean. That is **requires backend confirmation**
(§13.2). Until a distinct signal exists, a failing database surfaces as
`state.error` on the cycles request, which is correct behaviour and is what
must be implemented; the console must not guess a degraded state.

---

## 8. Page-by-page: filters, columns, disclosure

### 8.1 Filter card — progressive disclosure

One shared filter card shape (`FilterCard`, already specced in
`console-v2.md` §6.1) with a two-tier body. README §2: common filters
first, Advanced disclosure for less common settings.

**Common — always visible, in this order:**

| Control | Semantics (state these in the field help, not only in this doc) |
|---|---|
| Asset / pair (base) | Chip input. Multiple entries are OR'd. Exact symbol match, case-insensitive. Empty = every tracked base. Replaces the comma-separated text input. |
| Buy on / Sell on (exchange selection) | Two independent multi-select chip groups. A row qualifies when its buy venue is in *Buy on* **and** its sell venue is in *Sell on*. **An empty group means "every enabled venue"** — stated inline, because it is currently ambiguous. Entitlement-gated chips beyond `venues.screener_max` use the package state. |
| Quote asset | Multi-select, exact. `USDT`, `USDC`, `FDUSD` and `USD` are distinct and are never merged (`scanner-suite.md` §3). Default `Any`. |
| Min est. spread after fees (bps) | Compares against the after-fees figure, inclusive `≥`. Renamed from "Min spread", which did not say *which* spread it filtered. |

**Advanced — collapsed, toggle labelled `Advanced filters ({n} active)`
with the active count as a badge:**

| Control | Semantics |
|---|---|
| Base deny-list | Chip input. Deny wins over allow. |
| Min liquidity (quote) | Compares the min-of-both-sides top-of-book quote value. Rows whose liquidity is unknown are **never compared against a minimum** and are excluded unless the opt-in below is on (`scanner-suite.md` §3). |
| Min lifetime (s) | Seconds the spread has continuously stayed above the row's threshold. Lanes flagged as possible mismatches have no lifetime. |
| Include lanes with unknown liquidity | **Unsafe-lane opt-in.** Turning it on shows a persistent strip: `These lanes have no published size. They are skipped by alerts and by paper execution as LIQUIDITY_UNKNOWN.` |
| Include lanes flagged as possible asset mismatch | **Unsafe-lane opt-in.** Persistent strip: `The same ticker is not always the same asset on two venues. These lanes are skipped by alerts and by paper execution as SUSPECT_MISMATCH and have no lifetime.` |
| Fee overrides | **Not present on any list surface.** Overrides exist only on the Calculator (§8.3), where the result is explicitly a what-if. A list that mixes venue-table fees with hand-entered ones is not comparable. |

**Template row** (unchanged from `console-v2.md` §6.1): saved-template
select, `Save as template…` (inline name field), `Reset filters` (page
defaults, not the last-saved template). Template count consults
`rules.templates_max` where applicable.

**URL synchronisation (new, required for the round-trip task flow).** Every
filter serialises to a `/screener` query parameter; changes replace the
history entry for keystrokes and push for chip/numeric commits. Bare
`/screener` still resolves to page defaults, so this is not a URL change.
`console-v2.md` §6.1 already required "every filter change updates the
table's query params (shareable / bookmarkable URL)"; `/screener/page.tsx`
does not implement it today (no `useSearchParams`, no `router.replace`),
while `/calculator/page.tsx` does read query params. Implementing it
preserves an existing requirement and is what makes §10.2 work.

### 8.2 Cross-exchange spreads (`discover.spreads`, `/screener`)

**Stats strip — four default cells** (README §2: seven counters plus the
expanded form consumed the first viewport):

`Venues online (x / y)` · `Pairs tracked` · `Oldest row age` ·
`Poll interval`

`Spreads / sec` moves to the page's diagnostics disclosure.
`Excluded: suspect` and `Excluded: unknown liquidity` move into the
Advanced filter header as one line: `{n} lanes excluded as possible
mismatches · {n} excluded for unknown liquidity`, each a button that turns
on the corresponding opt-in.

**Default columns — seven:**

| # | Header (with unit) | Content | Notes |
|---|---|---|---|
| 1 | `Pair` | `{base}/{quote}` | Sticky first column; the row's accessible name |
| 2 | `Buy on` | venue name, ask price, age badge | Price to 2 significant decimal places more than the venue tick where known, else shortened per §8.6; age toned per the §3 canon |
| 3 | `Sell on` | venue name, bid price, age badge | Same |
| 4 | `Est. spread after fees (bps)` | signed, shortened, bold, primary sort | `+`/`-` sign always present; tone follows the sign, never hardcoded green (F14) |
| 5 | `Liquidity ({quote})` | quote amount, unit in the header | `Not published` for unknown, never `0` |
| 6 | `Limitations` | up to three text badges: worst-of the two ages (`STALE {age}` when >3× poll), `possible mismatch`, `liquidity unknown`, `networks unknown` | This is the column README §2 wanted visible beside the candidate instead of off-screen |
| 7 | `Detail` | opens the row drawer | Pointer affordance only — see the single-tab-stop rule below. Clicking or pressing Enter on the row itself does the same thing, so the action is never stranded off-screen on a narrow viewport (F19) |

**Single-tab-stop rule (applies to `/screener` and `/perpetuals`).** The
**row** is the one participant in the roving `tabindex` order (§11.2). The
`Detail` cell's control carries `tabindex="-1"` and exists as a pointer
affordance; it is never a second tab stop for the same action. The row's
accessible name (`{base}/{quote}, {buy venue} to {sell venue}`) plus the
drawer carry the action for keyboard and screen-reader users. Any other
per-row control that is *not* duplicated by row activation (there are none
in the default columns) would need its own explicit contract.

**Optional columns** — column picker button `Columns`, selections persisted
per user in `localStorage`, all off by default:

`Spread before fees (bps)` · `Lifetime (s)` · `Buy age (ms)` ·
`Sell age (ms)` · `Networks (withdraw / deposit)` ·
`Venue clock skew (ms)` *(requires backend confirmation, §13.5)* ·
`Est. net result at default size ({quote})` *(requires backend
confirmation, §13.5 — only `net_bps` and `liquidity_quote` are observed on
the list response)*

Networks badges (`open` / `closed` / `unknown (venue requires API key)`)
always render in the drawer, whether or not the optional column is on, with
the literal string rendered in the body rather than in a `title` (F16).

**Table footnote, once per table, verbatim:** `no-transfer, top-of-book`.

**Row drawer** (`discover.spreads.drawer`) — bounded to the viewport, no
horizontal overflow (README §3), grouped into four blocks:

1. **Identity** — `{base}/{quote}`, `{buy venue} → {sell venue}`, captured
   age per side.
2. **Feasibility and limitations** — every applicable limitation as a
   sentence, including the verbatim `suspect_reason` when present.
3. **Per-side quotes** — venue, price, size, age for each side.
4. **Exact values** — every shortened figure's full decimal string, in a
   selectable monospaced list. This is the "exact precision on demand" the
   README asks for, and it is a visible region, not a tooltip.

Footer: `Open in calculator` (prefills base, quote, both venues, and a size
defaulted to the row's liquidity cap) and `Close`. Focus contract in §11.4.

### 8.3 Spread calculator (`discover.calculator`, `/calculator`)

README §4: put the actual backend feasibility outcome before the estimate,
explain the limitation, label amounts with their asset, move overrides into
Advanced.

| Region | ID | Contents |
|---|---|---|
| A. Provenance | `calc.provenance` | When arrived at from a drawer: `Prefilled from a cross-exchange spread row: {base}/{quote}, buy {venue}, sell {venue}, quotes {age} old when opened.` Plus `Back to spreads` which returns to `/screener` **with the filters that produced the row**, via the query string from §8.1. |
| B. Inputs — common | `calc.inputs` | Base asset, Quote asset, Buy venue, Sell venue, `Size ({quote})`. Every numeric input's unit is in its label or suffix. |
| C. Inputs — advanced | `calc.inputs.advanced` | Collapsed disclosure `Advanced: fee and transfer overrides`. Contains `Transfer fee ({quote})`, `Override buy fee (bps)`, `Override sell fee (bps)`. When any is non-empty, a badge `What-if overrides active` renders next to the Calculate button and inside the result header. |
| D. Feasibility | `calc.feasibility` | **First region of the result, above any number.** Renders the backend's `liquidity_ok` outcome as a full sentence with the backend's verbatim reason: `Not executable at this size — {reason}.` or `Executable at this size under the stated model.` Tone `warn`/`bad` for not-executable, `ok` otherwise. |
| E. Estimate | `calc.estimate` | Heading `Estimate under the stated model`. `Est. net result ({quote})` leads, bold, signed; then `Est. net (bps)`; then the breakdown table `Before fees / Buy fees / Sell fees / Transfer fee / After fees`, each with its asset. Gross is subordinate, never equal weight. |
| F. Limitations | `calc.limitations` | Model captions verbatim (`no-transfer, top-of-book` unless a transfer fee was supplied), the size actually modelled, and the sentence: `An estimate is not evidence that this is executable or profitable.` |
| G. Exact values | `calc.exact` | Expandable list of every figure's full decimal string. |

No value in regions D–G is computed in the console. Every submit re-posts
to the calculator endpoint and renders the response.

### 8.4 Paper Trading tables

**Live simulations** — `Cycle` (id, monospaced) · `Triangle` ·
`Started` · `Age` · `Stage` · `Reserved capital ({asset})` · `Detail`.

**Simulation history** — default columns:

`Finished` · `Cycle` (monospaced id) · `Triangle` ·
`Outcome` (backend vocabulary verbatim, toned per §4.1) ·
`Net result ({asset})` signed · `Fees ({asset})` ·
`Duration (ms)` · `Detail` → `/cycles/[id]`.

Optional: `Started` · `Legs` · `Slippage (bps)` · `Revalidations` ·
`Config version` · `Rejection reason code`.

Filters: common — outcome, triangle, window. Advanced — starting asset,
config version, minimum net result. Server-side filter support is
**requires backend confirmation** (§13.3); until confirmed, filters apply
client-side over the fetched page and the region says so in one line:
`Filtering the {n} loaded simulations.`

**Rule simulations (`/auto-paper`)** — per-rule summary keeps every column
from `console-v2.md` §6.7 with two changes:

- Column 1 becomes `Rule`, rendering the display name on the first line and
  the exact identifier monospaced on the second with a copy button
  (README §6).
- A `Configure this rule →` action per row, targeting the rule's row on
  `/scanner-alerts`. A rule-scoped deep link (`/scanner-alerts?rule={id}`)
  is preferred; whether the page reads such a parameter today is
  **requires confirmation** (§13.3). Without it, the action navigates to
  `/scanner-alerts` and the region states `Opens the rules list`.

`Sample size (n)` stays its own column with the `thin` badge under n < 30.
`Skipped (top reason)` expands inline to the full breakdown. Both unchanged.

The page's introductory paragraph is replaced by one sentence —
`Rules can run their own paper simulations. Live execution stays disabled.`
— with the existing longer technical explanation moved into an expandable
`How rule simulations work` disclosure (README §6: technical introductory
copy made the relationship to Paper Trading harder to understand).

### 8.5 Data-age treatment (all live tables)

Unchanged canon from `console-v2.md` §3, restated as the implementation
contract:

| Age vs the row's poll interval | Tone | Cell text | Row |
|---|---|---|---|
| ≤ 1× | plain/dim | exact age, e.g. `1.8s` | normal |
| > 1× and ≤ 3× | `warn` | exact age, e.g. `6.4s` | normal |
| > 3× | `bad` | `STALE 11.2s` — the literal word, always | faint row dim (a different opacity value from the entitlement-gated state, so the two never read alike) |

Both sides of a spread row carry their own age and their own tone; a row
can be half fresh. Every live region additionally shows its own connection
state (`WS connected/connecting/closed` or `Updated Ns ago`) so a stopped
poll is never indistinguishable from a quiet market.

### 8.6 Decimal display

README §2 and §4: concise, exact-string-based display formatting, with raw
values preserved. The observed values are extreme — a net-bps cell of
`+1063.1123595505617977752808989` and a calculator size of
`56179.775280898876…`.

Binding rules:

1. **String operations only.** Shortening is a slice of the decimal string
   with optional zero-padding. No `parseFloat`, no `Number()`, no
   `toFixed`, no arithmetic, anywhere in a display path. `AGENTS.md`
   forbids floats in any value that reaches a decision; F8 is a live
   float-divide defect and F18 records that exact string percent↔fraction
   shifting already exists in `ScreenerShared.tsx` — extend those helpers,
   do not add a formatter that parses.
2. **Truncate, never round.** Rounding in the console would produce a
   number the backend never returned.
3. **Signal the truncation.** When digits are dropped the cell renders a
   trailing `…` (`+1063.11…`). No ellipsis means the value is complete.
4. **Default precision by kind:** bps 2 dp; quote/asset amounts 2 dp for
   quote currencies, 8 dp for base amounts, always with the asset symbol;
   percentages 2 dp; ages 1 dp seconds under 60s then `m:ss`; durations ms
   as integers.
5. **Exact value always reachable in a visible region** — the drawer's or
   panel's `Exact values` block, not a `title` (F16).
6. **Accessible name carries the state:** a shortened cell's
   `aria-label` is `"{shortened value}, exact value in row detail"`.
7. **Never truncate with CSS.** The truncated drawdown in README §1 is a
   layout failure: figures wrap or the column widens, and units are never
   clipped. `Stat` values that must ellipsize carry a `title` **and** the
   full value in an adjacent visible region.

---

### 8.7 Remaining surfaces — region order, default columns, filters

Compact spec for every surface not detailed above. All inherit: §7 state
rules, §8.5 data-age canon, §8.6 decimal display, §11 keyboard/mobile/a11y,
and the source-label chip from §5.4 where applicable. Units are part of the
column header, never assumed.

| Surface | Region order | Default columns (units in the header) | Common filters | Advanced filters |
|---|---|---|---|---|
| `/perpetuals` | stats strip (Venues with perps online · Contracts tracked · Mean funding interval · Oldest row age) → filter card → table → footnote → row drawer | `Venue` · `Base` · `Perp mark` · `Basis (bps)` · `Funding rate (per interval, %)` · `Est. carry APR after fees (%)` · `Limitations` · `Detail` | Venues (one group, not buy/sell) · Base chips · Quote asset · **`Min est. carry APR after fees (%)`** — this replaces the Screener's spread threshold; there is no spread filter here | Base deny-list. No liquidity or lifetime filter is specified: those fields are not established for perps rows (§13.5 discipline — do not add a filter for a field that may not exist) |
| `/funding` | base selector + venue chips + window picker → one labelled chart per selected venue → series table → limitations footnote | `Venue` · `At (UTC)` · `Rate (per interval, %)` · `Interval (h)` | Base asset (single select) · Venues · Window chips (24h / 72h / 7d / 30d) | none. **`/funding` does not reuse the whole filter card** — it has no spread, liquidity, lifetime or threshold filter, because it has no spread rows (`console-v2.md` §6.4) |
| `/alerts` | A. Unresolved summary by severity → B. Incidents table → C. Resolved incidents (toggle, same table) | `Severity` · `Opened` · `What happened` (backend summary verbatim, wrapped, never truncated) · `Source` · `State` · `Actions` | Severity chips · State chips (`active` / `acked` / `resolved`) | Source · Window |
| `/scanner-alerts` | A. Rules table → B. Create/Edit drawer → C. Event history, filtered to the selected rule | Rules: `Rule` (display name + monospaced id) · `Kind` · `Status` (`enabled`/`disabled`) · `Venues` · `Threshold` (with its kind's unit) · `Delivery` · `Rule simulations` · `Cooldown (s)` · `Actions`. Events: `Opened` · `Closed` · `Base/Quote` · `Venues` · `Peak net (bps or APR)` · `Lifetime (s)` · `Telegram sent` · `Rule simulation` (link, or `— skipped ({reason}) —`, never blank) | Kind · Status | Venue · Window |
| `/strategies` | A. Active version summary (version, applied at, by whom) → B. Parameter form grouped by section, units and backend effect chips per field → C. Version history and rollback | `Version` · `Applied at` · `By` · `Changed fields` · `Effect` · `Actions` (View diff / Roll back) | none | none. `risk.*` renders **read-only with an inline "changing risk limits requires ADMIN"** for OPERATOR — never hidden then 403 |
| `/pnl` | A. Source chip + window picker → B. Headline simulated results → C. Breakdowns → D. Charts → E. Evidence limitations (§5.4) | Headline: `Net after fees ({asset})` · `Fees ({asset})` · `Max drawdown ({asset})` · `Cycles (n)`. Breakdown rows: `Dimension` · `Cycles (n)` · `Net after fees ({asset})` · `Fees ({asset})` · `Hit rate (%)` · `Mean net (bps)` | Window · Starting asset | Venue · Triangle · Config version. Window options bounded by `history.retention_days` with the bound stated |
| `/screener-reports` | A. Source chip → B. Reports list → C. detail route: report sections + limitations | `Generated at` · `Window` · `Rules covered` · `Alerts` · `Executed` · `Net after fees ({quote})` · `Sample size (n)` · `Detail` | Window | Rule |
| `/reports` | A. Source chip + generate controls (`reports:generate`, read-only otherwise) → B. Reports list → C. Selected report rendered by section | `Kind` (daily / weekly) · `Period` · `Generated at` · `Status` · `Detail` · `Download` | Kind · Window | none |
| `/campaigns` | A. Recorder control → B. Recorded sessions → C. Campaign runs → D. Selected run detail | `Started` · `Recording` · `Config version` · `Status` · **`Verdict`** (full text, wrapped, never truncated, no verdict → `dim` not `ok`) · `Sample size (n)` · `Detail` | Status · Window | none |
| `/replay` | A. Recorded sessions (cross-links to `/campaigns`, not duplicated) → B. Replay and backtest runs → C. Config-version comparison | `Session` · `Captured window` · `Size` · `Config version(s)` · `Status` · `Actions` | Window | none. Where no console action exists yet, the column stays honestly labelled as reference rather than presented as an affordance |
| `/ai` | A. Source chip + pending recommendations → B. Recommendation detail → C. Approve / Reject with diff confirm → D. Past analyses | `Created` · `Scope` · `Recommendation` (one line, wrapped) · `Confidence` · `Sample size (n)` · `State` · `Actions` | State | Scope · Window |

Two rules that apply across this table:

- **Evidence and risks are body content, not tooltips.** On `/ai` the
  recommendation's evidence and risks render in the detail region and in
  the approve dialog body, not in a `title` on a truncated cell (F16).
- **Reject is confirmed like approve.** Both mutate a recommendation's
  state and both use `ConfirmDialog`; only approve additionally shows the
  config `DiffTable`.

---

## 9. Settings — three in-page categories

### 9.1 Categories and panel mapping

Sub-nav order: Account · Organisation · Billing · Notifications ·
Administration. The first four are the user's own scope; the fifth is the
deployment's.

| Category | ID | Panels (existing sections, unchanged in behaviour) |
|---|---|---|
| Account | `settings.account` | Session (user, role badge, sign out, change password); MFA status; Setup wizard link → `/onboarding`; Theme preference; Table column preferences (client-side, new); Security posture (read-only list) |
| Organisation | `settings.organisation` | Renders `/org`: organisation name, plan code, members and their `org_role`, seat usage vs `seats.max`, role changes and suspensions gated on `org_role` OWNER/ADMIN. **This is organisation administration and is never presented as platform administration.** |
| Billing | `settings.billing` | Renders `/billing`: current package, usage against entitlement limits, invoices, plan change. Keeps the `Live execution — Not offered` row verbatim. |
| Notifications | `settings.notifications` | `#notifications` routing form (per-severity → channel, cooldown seconds, with `params.go` bounds in plain units); channel availability from `alerts.channels`; `Telegram delivery →` (`/telegram`) as the delivery-status surface |
| Administration | `settings.administration` | `#operating-mode`, `#markets`, `#venues`, `#scanner-suite`, `#logging`, `#ai`, `#platform-versions`, `#users`, `#strategy-risk`, `#security` (write-only secrets vault). Header: the fixed deployment-scope paragraph from §5.7 |

Administration opens with the scope paragraph and is the only category
whose heading names the deployment. `platform_admin` and the per-section
permissions decide what inside it is editable; the backend decides what is
accepted.

The secrets vault panel stays **write-only**: values are submitted and
cleared, never returned, never logged, never re-displayed. No exchange
trading credential field exists in any category. Exchange access on this
platform is public market data only, and the Security posture list says so.

### 9.2 Anchor preservation

All nine existing ids must keep resolving on `/settings` after the
recategorisation: `#operating-mode`, `#markets`, `#scanner-suite`,
`#logging`, `#ai`, `#platform-versions`, `#users`, `#notifications`,
`#security`. Two are linked from the nav today (`#markets`, `#users`), one
from the restart banner (`#platform-versions`), and `#notifications` is
linked twice from `/telegram`.

Additive ids assigned to sections that have none:
`#session`, `#setup`, `#venues`, `#strategy-risk`, `#security-posture`.
New ids never replace an existing one.

Required hash behaviour (fixes F17 — anchors currently land before the
target has height):

1. On load or hash change, resolve the hash to its category and select that
   category first.
2. Scroll to the section **after** the section's own data request resolves,
   not on first paint. The handler re-runs once when the section reports
   ready.
3. Move focus to the section heading (`tabindex="-1"` then `.focus()`) so
   the landing is announced and keyboard position matches visual position.
4. An unknown hash selects Account and leaves the URL untouched. It never
   throws and never scrolls to nothing.
5. `scroll-margin-top` on every section accounts for the sticky
   `shell.mode` region.

### 9.3 Edit → preview diff → confirm → apply → feedback

Unchanged mechanics, specified here so the categorisation does not lose
them. Every versioned panel follows exactly this sequence:

| Step | Requirement |
|---|---|
| 1. Edit | Field-level inputs with units and the `params.go` bounds in plain language. Client validation is UX sugar; the backend's rejection always replaces the optimistic message and is mapped to its field via `aria-describedby` + `aria-invalid`. Sections the role cannot change render **read-only with an inline reason**, never hidden. |
| 2. Preview | `Preview changes` produces the backend's own preview/diff, never a client-computed one. |
| 3. Diff confirm | `DiffTable` inside `ConfirmDialog`: before value, after value, and the backend's `field_timing` chip per row (`Applies now` / `On restart`). A change with any restart-scoped field also carries the consequence sentence for a restart. |
| 4. Apply | Submitted with `parent_version` and CSRF. HTTP 409 `stale_version` renders `Someone else changed this while you were editing.` with both version numbers and a `Reload current version` action — the draft is not silently overwritten. |
| 5. Feedback | Toast naming the backend's own message; the version history table refreshes and the new version is highlighted; when a restart is now pending, the shell's `RestartBanner` appears with the backend's verbatim `pending_reasons`. Rollback stays available from the version table with the same diff-confirm flow. |

Non-versioned account actions (password change, theme, column preferences)
skip steps 2–3 and go straight to apply with inline feedback. A password
change is confirmed by the backend's response, never assumed.

---

## 10. Task flows

### 10.1 Check status → act

1. Land on `/overview`. `shell.mode` already answers "paper only", and
   `overview.status` answers connected / running-or-paused in the first
   six cells above the fold at 1280×720 and at 390px.
2. Read `overview.attention`. Zero items renders the healthy sentence
   `Nothing needs attention. Paper simulation is running and the market
   data feed is healthy.` — distinguishable from loading because loading
   shows `Loading attention items…`.
3. Act from the item's own link. The three most consequential acts are
   reachable without leaving Overview: pause/resume (inline in
   `overview.status`), acknowledge an alert (1 activation to `/alerts`),
   check safety (1 activation to `/risk`).
4. If a restart is pending, `RestartBanner` in `shell.mode` carries the
   backend's reasons and the type-to-confirm `RESTART` dialog, unchanged.

### 10.2 Find a candidate → understand it → calculate it → return with filters intact

1. `Discover` (1 activation) lands on `discover.spreads`.
2. Set common filters. Each change writes the query string, so the URL is
   now the shareable state of this search.
3. Scan the table. The `Limitations` column is in the default seven, so
   freshness and caveats are beside the candidate rather than off-screen.
4. Activate the row (click, Enter, or the `Detail` action) — the drawer
   opens, the route does not change, focus moves to the drawer's close
   button.
5. Read `Feasibility and limitations`, then `Per-side quotes`, then
   `Exact values` for full precision.
6. `Open in calculator` navigates to `/calculator?…` with the row's
   parameters. `calc.provenance` states where the values came from and how
   old the quotes were.
7. `Back to spreads` — or the browser Back button — returns to `/screener`
   **with the query string intact**, so the filtered list is restored.
   Today this loses the filters because they live only in component state.
   The drawer reopens only on explicit re-activation; the scroll position
   is restored by the browser's own history behaviour.

### 10.3 Inspect a simulation

1. `Paper Trading` (1 activation). `paper.safety` confirms the engine state
   and offers `Risk and safety →`.
2. `paper.live-table` for what is running now; `paper.history` for what
   finished. Both are on the landing surface, above the reset disclosure.
3. Activate a history row → `/cycles/[id]` (2 activations from Overview).
4. On the cycle page: outcome in backend vocabulary, per-leg economics,
   linked orders and fills, the exact cycle and triangle identifiers, and
   `Simulated orders →` / `Simulated fills (this cycle) →` cross-links.
5. For a rule-driven simulation, start at `paper.rules` instead; the row's
   `Configure this rule →` reaches the rule that produced it.

### 10.4 Find results with evidence limitations

1. `Research & Results` (1 activation) lands on `research.pnl` with the
   source chip `Source: triangular paper engine ledger`.
2. Read the aggregate. Every rate or percentage has its `n` in the same
   glance; no percentage exists without its sample size.
3. Read `research.limitations`, always the last region and never collapsed:
   sample sizes, window, regime coverage (or the explicit statement that
   regime coverage was not recorded), the `docs/campaigns/…` path, and the
   verbatim paper-performance sentence.
4. For screener-rule evidence, switch to `research.screener-evidence` — a
   different source chip, a different engine, and the sub-nav makes the
   switch one activation.
5. Operational reports (`research.ops-reports`) are labelled as a separate
   system so they are never read as performance evidence.

### 10.5 Change my preferences vs administer the platform

- **My preferences:** account menu or `Settings` (1 activation) → Account.
  Password, theme, table columns, MFA status, setup wizard. Nothing here
  affects anyone else.
- **My organisation:** `Organisation` (1 activation, footer-pinned, or the
  Settings sub-nav). Members, roles, seats. Gated on `org_role`. Labelled
  as the organisation's scope.
- **My plan:** `Billing` (1 activation, footer-pinned). Package, usage
  against limits, plan change.
- **The deployment:** `Platform administration` (1 activation) → the scope
  paragraph, then Risk and safety, System health, Exchange feeds, Audit
  log, and `Platform configuration` which opens `/settings` Administration
  at the same anchors it has today.

The two administration surfaces are never adjacent in the nav and never
share a heading word beyond "administration", which is always qualified
("Organisation", "Platform").

---

## 11. Keyboard, mobile, accessibility

### 11.1 Mobile behaviour (< `md`)

- Top bar keeps brand, notification bell, theme, hamburger. Second row
  keeps the mode chip and the scoped paper control. Both persist on every
  page at every width — neither waits for the menu to open (F1, F2).
- Primary nav is the existing full-height overlay with **full labels, never
  icon-only**. Escape closes it and focus returns to the hamburger
  (already implemented; do not regress).
- Sub-nav is a horizontally scrollable strip with scroll-snap; the active
  link is scrolled into view on mount and marked `aria-current="page"`.
- Read-and-acknowledge surfaces reflow to one column: Overview (status two
  columns, attention/summary one), Alerts, Rule simulations summary,
  Research cards, notification panel.
- Dense tables keep their shape and scroll horizontally with the first
  column sticky and a visible scroll affordance. The row itself is the
  detail activation, so the action is never stranded in an off-screen last
  column (F19). The drawer is full-screen at this width.
- Dense editing surfaces (rule create/edit, calculator advanced
  overrides, Administration panels, onboarding balance grid) show the
  existing notice rather than cramming: *"Editing is easier on a wider
  screen — you can still read the current values below."*
- Stat grids never exceed two columns below `md` and never six at `md`
  (F19).
- Touch targets: 44×44 CSS px minimum for nav rows, sub-nav links, filter
  chips, column-picker items and row actions. Text floor 11px.

### 11.2 Keyboard

| Context | Contract |
|---|---|
| Page entry | A `Skip to main content` link is the first focusable element on every page (new; WCAG 2.4.1). |
| Focus order | skip link → top bar controls → `shell.mode` controls → primary nav → sub-nav → page regions in DOM order. |
| Primary nav | Native links. Tab moves through them; `aria-current="page"` on the active destination and on the active sub-view. |
| Sub-nav | Native links inside `<nav aria-label="{Destination} sections">`. **Not** a tab widget — these change the route, so tab roles and arrow-key semantics would misdescribe them. |
| Tables | Roving `tabindex` across rows, **one tab stop per row** (§8.2 single-tab-stop rule). Up/Down moves row focus, Home/End jump, Enter or Space opens the row drawer or navigates to the row's detail route, Escape from the drawer returns focus to the row. |
| Filter chips | Real `<button aria-pressed>`; Space and Enter both toggle (F15). |
| Column picker | Menu button with `aria-expanded` and `aria-controls`; checkbox items; Escape closes and returns focus to the button. |
| Drawers | `role="complementary"`, labelled by the row identity, focus to the close button on open, focus back to the row on close, Escape closes, background stays interactive (non-modal). |
| Dialogs | `ConfirmDialog` unchanged: focus-trapped, Escape cancels, click-outside disabled for destructive confirms, focus returns to the trigger (F15 fix). |
| Anchors | Focus moves to the target section heading after its data resolves (§9.2). |
| `/` | Focuses the current page's primary filter input when no text input has focus. |
| Command palette | `Ctrl/Cmd+K` (§11.5). |

### 11.3 WCAG AA criteria, with the specific obligations

| Criterion | Obligation in this spec |
|---|---|
| 1.3.1 Info and relationships | `scope="col"` on every `<th>`; a visually hidden `Actions` header for action columns; each table labelled by its region heading; `<fieldset>`/`<legend>` around each filter chip group |
| 1.4.1 Use of colour | Every state has a text carrier: `STALE {age}`, explicit `+`/`-` signs, badge words, `Not published`, `possible mismatch`. No state is colour-only |
| 1.4.3 Contrast (minimum) | ≥4.5:1 for all text including dim secondary text, badge text and the `…` truncation marker, measured in **both** themes |
| 1.4.4 / 1.4.10 Resize and reflow | Usable at 200% zoom and at 320px CSS width without loss of function. Overview, Alerts, Paper and Research reflow to one column. Dense data tables may scroll horizontally — the permitted data-table exception — and must keep a sticky first column and a visible affordance |
| 1.4.11 Non-text contrast | ≥3:1 for table borders, chip outlines, focus rings, the sub-nav active indicator, toggle states and the row focus indicator |
| 1.4.12 Text spacing | No fixed-height text containers in stat cells or table cells; figures wrap rather than clip (§8.6 rule 7) |
| 2.4.1 Bypass blocks | Skip link on every page |
| 2.4.3 Focus order | As §11.2; the nav overlay and drawers do not strand focus |
| 2.4.4 / 2.4.9 Link purpose | No bare `monitor →`. Link text names the target and object: `Monitor live simulations`, `Review 2 unresolved alerts` |
| 2.4.6 Headings and labels | One `h1` per page; one `h2` per region; every input has a visible label with its unit |
| 2.4.7 Focus visible | Focus ring specified against `--bg`, `--bg-panel`, `--bg-raised` in both themes. New elements requiring measurement: sub-nav links, filter chips, column-picker items, disclosure toggles, drawer controls, table rows under roving tabindex, entitlement-gated locks |
| 2.5.8 Target size (minimum) | 24×24 CSS px minimum everywhere, 44×44 for the touch targets in §11.1 |
| 3.3.1 / 3.3.2 / 3.3.3 Errors, labels, suggestions | `aria-invalid` plus `aria-describedby` on every invalid field; `role="alert"` on submit errors; bounds stated in plain units as the suggestion |
| 4.1.2 Name, role, value | Icon-only buttons carry `aria-label`; chips expose `aria-pressed`; disclosures expose `aria-expanded`; entitlement locks carry `aria-label="Included in {Package} — upgrade"` on the glyph itself |
| 4.1.3 Status messages | `aria-live="polite"` on live tables, the attention region and `Updated Ns ago`; `role="status"` on connection state; `aria-live="assertive"` reserved for a newly arrived CRITICAL alert |

Charts keep their `aria-label` summary including `n`, and the underlying
table remains the accessible source — a chart is never the only way to get
a number off a page.

### 11.4 Drawer and detail-page choice

Unchanged from `console-v2.md` §4: drawers for Screener and Perpetuals rows
(small, disposable glance, virtualisation-safe), dedicated `[id]` pages for
Triangle, Opportunity, Cycle and Screener report (enough content to want a
shareable URL). Both patterns stay; neither is unified into the other.

### 11.5 Command palette

`Ctrl/Cmd+K` opens a route jumper listing every destination and every
contextual surface by its explanatory label, plus recent routes. It is
`role="dialog" aria-modal="true"` with a combobox and
`aria-activedescendant`; Escape closes and returns focus.

It is an accelerator, never the only path: every route in §4 remains
reachable through the nav and sub-nav without it. It is the mitigation for
surfaces that move from 1 to 2 activations, and it is our own design.

---

## 12. What this does NOT change

- **Financial formulas.** Nothing here alters spread, net, liquidity,
  lifetime, carry, basis, fee, slippage, depth or quantization maths. The
  console renders backend strings and formats them with string operations
  only (§8.6).
- **Returned numbers.** No figure is recomputed, re-rounded or re-derived.
  Shortening for display is truncation of a string, signalled with `…`,
  with the exact value always available in a visible region.
- **Risk policy.** The deterministic risk engine stays the only gate.
  Nothing here adds, relaxes or bypasses a limit, a breaker policy or a
  revalidation. Risk status and the risk destination remain discoverable
  from Overview and from Paper Trading in one activation.
- **Live-execution restrictions.** Live order submission stays disabled by
  construction. `LIVE` remains absent from the operating-mode choices, the
  `LIVE is not an option` copy stays, Billing keeps `Live execution — Not
  offered`, and the paper-only mode indicator is persistent at every width.
  Nothing in this document brings live execution closer; the production
  gate is unchanged.
- **Authorization semantics.** Every route keeps the permission it has
  today. Grouping surfaces under a destination never adds a gate, and
  `operator-admin` is not gated on `platform_admin` (§3.3). RBAC remains
  backend-enforced; the console only hides what a role cannot use.
- **URLs and history.** No route is rewritten, redirected, aliased or
  removed. All nine existing `/settings` anchors keep resolving. Back and
  forward behaviour is unchanged, except that `/screener` filters become
  restorable through the query string.
- **Credentials.** The secrets vault stays write-only; no exchange trading
  credential field is introduced anywhere.
- **Honest language.** Nothing is described as guaranteed, risk-free or
  certain. Verdicts, risk outcomes, skip reasons and backend errors render
  verbatim. Every published aggregate cites `docs/campaigns/`.
- **Confirmation strength.** Paper reset keeps type-to-confirm `RESET`,
  ADMIN-only and paused-only. Engine restart keeps type-to-confirm
  `RESTART`. Versioned config keeps its diff confirm. Moving the reset
  control below the working regions changes its prominence, not its
  safeguards.

---

## 13. Requires backend confirmation

Marked as such rather than specified as fact, per the brief.

1. **Pause scope.** Whether `POST /api/v1/paper/pause` also halts
   rule-driven auto-paper executions, or only the triangular engine. Until
   confirmed, §2.2's dialog makes no claim about auto-paper and rule
   simulations are described as per-rule. README §6 names this explicitly.
2. **Database degraded signal.** Today the console has
   `recordings.list().persistence: boolean` — configured or not. A
   configured-but-failing database is not distinguishable from it. §7.4
   therefore routes failures through the request's own error state. A
   distinct health signal would let the empty state say "storage is
   configured but currently unavailable".
3. **Server-side filters and rule deep links.** Whether the paper cycles
   list accepts outcome / triangle / window / config-version filters
   server-side (§8.4), and whether `/scanner-alerts` reads a
   `?rule={id}` parameter (§8.4). Both have safe fallbacks specified.
4. **Attention feed.** §5.1 region B is specified as a client-side
   composition over **existing endpoints**, not a new one: active alert
   count, breakers open, `engine.status().restart`, feed health,
   `me.risk_ack_required`, and `billing.subscription().status`. Five of the
   six are already polled on `/overview`; **`billing.subscription` would be
   a new request on this page** — a deliberate addition, called out here
   against `ui-ux-audit.md` F20 (request fan-out), and droppable if the
   lead prefers to keep Overview's request count flat, in which case the
   `past_due` attention item is simply absent. No ranked-attention endpoint
   exists and none is assumed; if a backend ranking is preferred later it
   replaces the composition without changing the region's shape.
5. **Optional Screener columns.** `Est. net result at default size
   ({quote})` and `Venue clock skew (ms)` are listed as optional columns
   but only `net_bps`, `gross_bps`, `liquidity_quote`, per-side ages,
   lifetime and network status are observed on the list response. Neither
   column ships unless the field exists; the console must not derive
   either.
6. **Backend copy containing code symbols.** The SHADOW unavailability
   reason is backend-authored
   (`internal/platform/modes.go:21`: `"shadow execution is not wired into
   Engine.Run; enabling it changes the paper→portfolio result path
   (separate task)"`). §6.1 renders it verbatim behind a console-authored
   plain lead line. Softening the jargon requires a backend copy change and
   a test update (`internal/platform/expansion_test.go:34` asserts on the
   substring); that is a backend decision, not a console rewrite.
7. **Still open from `console-v2.md` §13, unchanged:** `screener_settings`
   field timing; the package→limit table; whether regime tags exist on
   paper cycles; the upgrade/billing route target for gated CTAs.

---

## 14. Open points for the lead

1. **`operator-admin` visibility axis.** `/risk` and `/system` are
   VIEWER-readable in `internal/auth/rbac.go`, so an area labelled
   "Platform administration" is visible to every current role. §3.3 keeps
   today's reachability exactly, which is the stated hard constraint, at
   the cost of a label that is broader than the read access inside it.
   The alternative — gating the area on `platform_admin` — would remove
   VIEWER and OPERATOR access to risk and system status and is therefore
   not specified. Confirm the label, or confirm a second label for the
   read-only safety surfaces.
2. **Activation regression on low-frequency routes.** `/telegram`,
   `/audit`, `/exchanges` and the Discover long tail move from 1 to 2
   activations. §4 documents the reason and §11.5 adds the palette as the
   accelerator. If any of these is more frequent than assumed, promoting it
   to a second entry point (as `/risk` and `/portfolio` already have) is
   the cheapest fix.
3. **Command palette scope.** Specified as an accelerator only. If it is
   out of scope for this pass, the activation counts in §4 stand on their
   own and nothing else in this document depends on it.

---

## 15. Consequences for QA and for existing components

This IA invalidates assumptions that are currently written into the shell
and relied on by the e2e suite. Naming them here so they are planned work
rather than a CI surprise.

| What changes | Where it is relied on today | Required follow-up |
|---|---|---|
| The **Scanner Suite nav group is removed**. Its seven entries become `discover.*` and `paper.rules` sub-views at 2 activations, so they are no longer all visible from a fresh session's sidebar | `ConsoleShell.tsx:160-163` and `578-581` both cite `e2e/console.spec.ts`'s *"nav group renders and every Scanner Suite page loads"* as the reason groups default to expanded with no forced accordion | Rewrite that spec to assert each route **loads**, reached through its destination + sub-nav (or directly by URL), rather than asserting sidebar link visibility from the landing page. The routes themselves are unchanged, so the load assertions stay valid |
| **Group collapse persistence is retired** for destinations; expansion is derived from the active route | `GROUP_STORAGE_KEY = "arb.nav.collapsed-groups"`, `loadCollapsedGroups`/`saveCollapsedGroups`/`useCollapsedGroups` in `ConsoleShell.tsx` | The key is left unread rather than migrated or cleared, so a rollback keeps working. Any e2e assertion on chevron/collapse state is removed. No user data is lost — it was presentation state only |
| **`IconRail` and `NavGroupHeader` become unused** by the shell (§1.1 supersedes `console-v2.md` §2.1) | `components/IconRail.tsx`, imported by `ConsoleShell.tsx` | A design decision, not a deletion mandate: the frontend engineer decides whether to delete or leave them unreferenced. If the glyph set is reused for the six destination rows, the glyph module survives even if the rail component does not. Either way, nothing copied from any other product enters the console |
| **`/screener` gains query parameters** | `screener/page.tsx` holds filters in component state only | New e2e coverage: set filters → navigate to `/calculator` → Back → filters restored. Also assert bare `/screener` still resolves to defaults, so the URL contract is additive |
| **`/settings` categories + anchor scroll-after-load** | Nine existing ids; `ConsoleShell.tsx:79,83,496` and `telegram/page.tsx:28,63` link to four of them | Regression coverage for all nine anchors plus the five new ids, asserting the target section is scrolled to **and focused** after its data resolves (F17) |
| **Overview region reorder and the collapsed diagnostics disclosure** | `overview/page.tsx` region order and `statOrError` per-cell degradation | Keep the per-cell degrade assertions; add one that the diagnostics disclosure is collapsed on a fresh profile and that every counter currently visible is still reachable inside it. No counter is deleted |
| **Role and entitlement matrix unchanged** | `internal/auth/rbac.go`, `lib/auth.tsx` `can()`, `useEntitlement` | The most valuable regression in this pass: for each of VIEWER / OPERATOR / ADMIN, assert every route in §4 resolves to the same outcome (render / read-only / 403) as before the IA change. §3.3 claims no gate moved; this is the test that proves it |
