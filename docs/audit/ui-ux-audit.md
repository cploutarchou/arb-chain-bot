# Console UI/UX audit

Audited tree: `master` at `abddb55` (2026-09-09). Static review of `web/src`
(36 pages, 18 components, 9 lib modules) against
`docs/design/console-ux-audit.md`, `docs/design/ux/console-v2.md`,
`docs/design/ui/design-system.md` and `docs/design/scanner-suite.md` §5, and
against the backend vocabulary the pages render. No file was changed during
the review.

No P0 was found: nothing renders paper as live, and no pre-fee figure is
presented as "profit". The highest-severity problems are mislabelled money
semantics, a cycle-state vocabulary that does not match the backend, and a
monitoring/emergency surface thinner than the dashboard copy implies.

## Ranked findings

### F1 · P1 · Mode banner absent from the mobile chrome
- Evidence: `web/src/components/ConsoleShell.tsx:733` — the mobile top bar (`md:hidden`) holds only the wordmark, `NotificationBell`, `ThemeToggle` and the menu button (734–749). `ModeBanner` is mounted only inside the overlay `<aside>` (763–767, rendered only while `mobileOpen`) and the desktop sidebar (777, 788–791). `RestartBanner` sits in `<main>` (796) and does appear on phones; the mode banner does not.
- Impact: console-v2 §2.1 calls the banner "always visible"; below 768 px an operator on `/paper` or `/portfolio` gets no persistent PAPER signal and a REPLAY session's "not live" warning is hidden entirely.
- Action: mount a compact `ModeBanner` (dot + mode word) in the mobile top bar between the wordmark and the bell, reusing the hoisted `modeState` (723).
- Validation: Playwright at 390 px asserting the mode text is visible on `/overview`, `/paper`, `/portfolio` without opening the menu; repeat with a mocked `REPLAY` mode.

### F2 · P1 · No emergency "stop new trades" control; pause is 2–3 clicks deep, unconfirmed, unexplained, silent on success, generic on failure
- Evidence: the only pause control is `web/src/app/paper/page.tsx:93-95`; `control()` (24–31) renders nothing on success and on any failure sets "Control action failed (role or mode)." (29), discarding `ApiError.message`. Nothing on the page says what happens to `active_simulations` (86) when paused. Pause and Resume are both always enabled regardless of `s.paper.running` (93–98). Overview "Quick actions" (`overview/page.tsx:291-298`) are links (`QuickLink`, 303–312): "Paper Trading →" navigates, it does not pause. The shell has no global control.
- Click count: desktop 2, mobile 3; at 390 px the button row (92–104) follows six stacked `Stat` cells, so it is very likely below the first viewport.
- Impact: in an incident the trader must find a page and a secondary-styled button; a failed pause reads as a permissions problem; nothing confirms that in-flight simulations settle rather than cancel.
- Action: shell-level "Pause new cycles" control (RBAC-hidden via `can()`, present in the mobile top bar), one-sentence consequence copy ("in-flight simulations settle; no new cycles start"), success toast with the returned `running` state, backend message verbatim on failure, state-aware enablement.
- Validation: Playwright at 390/1024: from `/alerts`, reach pause in one click; mock 403/409 and assert the backend message renders; axe pass on the control.

### F3 · P1 · Opportunity detail labels a fee-inclusive figure "Gross", gives it equal weight ahead of Net, leaves a negative net untoned
- Evidence: `web/src/app/opportunities/[id]/page.tsx:49-54` renders "Gross final", "Estimated final", "Gross profit", "Net profit", "Gross return bps", "Net return bps" as identical `Stat` cells, gross first. Backend semantics: `internal/pricing/pricing.go:65` `GrossProfit // FinalAmount - InputConsumed (fees already inside)`; `internal/opportunity/opportunity.go:67-72` `NetProfit // EstimatedFinal - InputConsumed`, `GrossReturnBps // Quote.ReturnBps (fees in, buffers out)`. The gross→net gap is the latency+risk buffer, not fees, and neither fees nor `BufferAmount` are itemised — legs are `JSON.stringify(d.legs, null, 2)` (63–67). Line 52 tones only a positive net; a negative net has no tone and no explicit sign. Screener and Perpetuals place "Gross bps" before "Net bps" (`screener/page.tsx:425-426`, `perpetuals/page.tsx:127-128`); the Calculator shows Gross before Net (`calculator/page.tsx:208-216`) without dimming gross (design-system §1.8).
- Impact: a trader reading "Gross 20 / Net 12" infers fees of 8 when fees were already inside the 20 and the 8 is buffers.
- Action: relabel "After fees, before buffers" and "Net (after buffers)"; render Net first, bold, via `signedText`/`signTone`; dim gross; add a Buffers row (`BufferAmount` exists server-side; expose it on `OpportunityDetail`) and a per-leg table with fee per leg (fields exist in `pricing.LegQuote`).
- Validation: copy review against `pricing.go`; unit test that a negative `net_profit` fixture renders the negative tone with a leading "-".

### F4 · P1 · Cycle-outcome vocabulary does not match the backend
- Evidence: backend outcomes (`internal/execution/executor.go:26-35`): `ALL_FILLED`, `LEG1_PARTIAL`, `LEG1_FILLED_LEG2_FAILED`, `LEG1_LEG2_FILLED_LEG3_FAILED`, `PARTIAL_CYCLE`, `TIMEOUT`, `EXPIRED`, `REJECTED` (wire value confirmed by `internal/storage/opportunity_detail_test.go:83`). The UI tests opportunity-status words instead: `triangles/[id]/page.tsx:119` and `opportunities/[id]/page.tsx:114` tone `"COMPLETED"`/`"FAILED"`, so every real outcome, including `LEG1_FILLED_LEG2_FAILED`, renders grey. `paper/page.tsx:161` gives a completed `LEG1_PARTIAL` and a stranded leg-2 failure the same amber. No page glosses the code or shows the backend `Reason`.
- Impact: a cycle that left inventory in an intermediate asset looks like a benign warning on Paper and like "nothing notable" on the detail pages.
- Action: one shared `cycleOutcomeTone()` keyed to `executor.go` (`ALL_FILLED` ok; `LEG1_PARTIAL`/`PARTIAL_CYCLE` warn; `*_FAILED`/`TIMEOUT`/`REJECTED`/`EXPIRED` bad) with a one-line gloss per code; add `reason` to `CycleRow` (backend) and render it.
- Validation: unit test each constant → tone; fixture page with `LEG1_FILLED_LEG2_FAILED` renders the critical tone plus gloss on all three pages.

### F5 · P1 · Overview fails the five-second test on PnL, risk/breakers and feed state
- Evidence: `web/src/app/overview/page.tsx:80-86` polls system status, scanner status, recordings, alerts, health, campaigns, portfolio — never `api.pnl()` or `api.risk()`. "Exchange health" (198–228) shows cumulative counters only (frames, reconnects, REST errors, resyncs, sequence gaps) — no connected/degraded/disconnected/rate-limited state and no book age (those live on `/exchanges`). "Paper engine" renders "N/A" untoned when `s.paper` is absent (100–107) with no reason. "Current" lists available/reserved for the first six assets (167–195) with no capital-in-use total.
- Present: mode; scanner READY/NOT READY; paper RUNNING/PAUSED; recorder; unresolved alerts (linked); database; session counters; active simulations. Missing: realized PnL today/24 h, drawdown vs limit, breaker board, feed connection/staleness, capital-in-use total, an explanation for N/A.
- Action: add PnL (realized, fees, drawdown per start asset) and risk (breakers open, bad when > 0) cells to the status strip; a feed cell derived from `ready` and book states ("CONNECTED / DEGRADED (2 books STALE) / DISCONNECTED"); replace N/A with "not running — mode is {mode}".
- Validation: five-second test with three traders on a screenshot; Playwright asserts the strip cells.

### F6 · P1 · Paper Trading is not a live-cycle monitor
- Evidence: `web/src/app/paper/page.tsx:85-90` engine stats only; "Active sims" is a count. Cycles table head (157) `Started, Outcome, PnL, Slippage bps, Cycle` — no leg 1/2/3 state, expected vs realized, duration, fees or reason; `settled_at` exists on `CycleRow` (`web/src/lib/api/client.ts:393`) and is not rendered. Orders table (174) omits `OrderRow.id` (client.ts:397) and remaining quantity. `CycleRow` (384–394) has no reason/leg fields.
- Impact: the page the nav calls "Paper Trading" cannot answer "what is executing right now and where is it stuck".
- Action: active-cycles region (poll or WS) with leg badges, elapsed, expected net vs realized; cycles table adds Fees, Duration, Reason, Opportunity link; orders add Order ID and Remaining; backend adds `reason` and a leg summary to `CycleRow`.
- Validation: fixture with an in-flight cycle at leg 2; Playwright asserts leg badges and elapsed tick.

### F7 · P1 · Request failures rendered as empty states or swallowed
- Evidence: `web/src/app/paper/page.tsx:62-69` `catch { setOrders({ cycle: cycleID, rows: [] }); }` → "No orders for this cycle." (`components/ui.tsx:161-163`) on a 401/403/500/network failure. `web/src/app/replay/page.tsx:126-133` `catch { setDetail(null); }` — a failed "View" shows nothing.
- Impact: a trader concludes a settled cycle placed no orders; a replay result silently refuses to open.
- Action: explicit error branch rendering `ErrorBox` with status/code; disable the button while loading.
- Validation: mock a 500 on the orders route and assert `ErrorBox` text.

### F8 · P1 · Onboarding float-divides a persisted rule threshold
- Evidence: `web/src/app/onboarding/page.tsx:265-268` `min_carry_apr: String(Number(threshold) / 100)` persisted via `api.screener.rules.create`; `web/src/app/perpetuals/page.tsx:62-64` `Number(minCarryApr) / 100` for the query. The exact helper exists and is documented in `web/src/components/screener/ScreenerShared.tsx:288-291, 329-331` (`percentToFractionStr`) and is used correctly by `scanner-alerts/page.tsx:154-161`. A typed "1.1" persists `"0.011000000000000001"`.
- Impact: this threshold gates automatic paper execution; decimal-money is non-negotiable.
- Action: use `percentToFractionStr` in both places; add a lint rule banning `Number(` on identifiers matching `*_bps|*_apr|*_quote|profit|pnl` under `web/src/app`.
- Validation: unit test `"1.1"` → `"0.011"`; grep for the banned pattern in CI.

### F9 · P2 · No data-age indicator on polled money tables; transient errors blank the table; book ages shown raw
- Evidence: `web/src/lib/usePoll.ts:11-12` "errors keep the last good data out of view"; no REST page shows "Updated Ns ago" except Screener's `Data age` (`screener/page.tsx:245`, computed per render). Book ages render as raw numbers with no threshold or STALE word: `exchanges/page.tsx:42`, `system/page.tsx:123`, `triangles/[id]/page.tsx:52`. The STALE canon exists only in `ScreenerShared.tsx:92-129`.
- Action: `usePoll` returns `lastOkAt` and a `stale` flag; pages render "Updated 4s ago" and keep the last data under a warn banner on a transient error; apply `ageCellText` to book ages against `risk.max_book_age_ms`.

### F10 · P2 · Opportunities list: partial status filter, REJECTED toned neutral, missing columns, legs never tabulated
- Evidence: `web/src/app/opportunities/page.tsx:10` `STATUSES = ["", "QUALIFIED", "REJECTED", "EXPIRED"]` vs nine backend statuses; badge (45) tones rejected neutral and expired/completed/failed all amber; head (38) lacks Exchange, Age/Expires (`expires_at` on the row type, unused), gross/fees/buffers, depth/max size; "Net profit" (50) has no asset; fixed `limit 100`, no paging; detail legs are a JSON dump.
- Action: columns Exchange, Expires/Age, Net profit with asset, Reason gloss; triangle/date/exchange filters; tones REJECTED bad, EXPIRED dim, FAILED bad; render legs as a table.

### F11 · P2 · Strategy parameters lack units, defaults and risk implication; two fields mix fraction inputs with percent help text
- Evidence: `web/src/lib/strategyFields.ts` labels carry no unit; `max_capital_utilization` is `min: 0, max: 1` with help "greater than 0% and at most 100%" (173–184); `min_data_quality` `0..1` with help "between 0% and 100%" (262–272). `FieldRow` (`strategies/page.tsx:73-100`) shows no default, current-value hint or implication. Risk Center's limits table is raw keys (`risk/page.tsx:22-28`).
- Action: unit suffix inside the field (bps, ms, start asset), accept percent where the help says percent (string-shift conversion), show default and a one-line consequence; share the label/unit map with Risk Center.

### F12 · P2 · Risk Center omits capital at risk, exposure, daily loss vs budget, partial fills and stale feeds
- Evidence: `web/src/app/risk/page.tsx` renders limits (22–28), breakers (29–42), session reject counts (43–51) and the timeline (56–105). Exposure and per-asset `daily_loss`/`drawdown` live on `portfolio/page.tsx:29-65` and are never placed beside `max_daily_loss`/`max_drawdown`.
- Action: top strip — breakers open, daily loss used / budget per asset, drawdown / max, unmarkable exposure, books STALE; link reject codes to the opportunities filter.
- Note: the backend never feeds daily loss or drawdown into the risk gate today (see `execution-risk-audit.md`); the UI gap and the backend gap must be closed together or the strip would show a limit that cannot trip.

### F13 · P2 · History has no cycle detail; cycle links dead-end at `/orders?cycle=`
- Evidence: `triangles/[id]/page.tsx:108`, `opportunities/[id]/page.tsx:106`, `fills/page.tsx:185` link to `/orders?cycle=`; no `/cycles/[id]` route exists. Orders/Fills filters (`orders/page.tsx:89-162`) are symbol/triangle/cycle/status/from/to; Paper's cycle list has no filters.
- Action: `/cycles/[id]` timeline (detected → validated → legs with fills → reconciliation → final PnL), reused from Paper, Triangle and Opportunity; outcome and session filters.

### F14 · P2 · Scanner hard-codes "Net bps" green regardless of sign and labels net profit as bare "Profit"
- Evidence: `web/src/app/scanner/page.tsx:283, 319` `<Badge tone="ok">`; heads 275/311 label the column "Profit" although the field is `net_profit`; client-side sort parses bps with `Number()` (140–147).
- Action: `signTone`/`signedText`; label "Net profit ({asset})"; decimal-string comparator.

### F15 · P2 · Accessibility gaps
- Evidence: filter chips are buttons with colour-only selection and no `aria-pressed` (`opportunities/page.tsx:21-33`, `alerts/page.tsx:32-42`, `triangles/page.tsx:31-41`, `risk/page.tsx:58-68`, `pnl/page.tsx:30-40, 66-76`); mobile overlay (`ConsoleShell.tsx:751-775`) has no Escape handler, focus move or trap; `ConfirmDialog` (`ui.tsx:453-516`) traps focus but never returns it; `<th>` without `scope="col"` and empty action headers (`ui.tsx:229-234`); no `aria-invalid`/`aria-describedby`/`role="alert"` anywhere in `web/src`; `text-[10px]` sentence text at `alerts/page.tsx:60` (floor 11 px); `Stat` truncates without `title` (`ui.tsx:58`).
- Action: shared `ChipGroup` with `aria-pressed`; Escape/focus-trap/return-focus; `scope="col"` and a visually-hidden "Actions" header; `aria-describedby` + `aria-invalid` in `FieldRow`; `role="alert"` on submit errors; 11 px minimum.

### F16 · P2 · Contracted literals and decision-critical text hidden in `title` tooltips
- Evidence: `ScreenerShared.tsx:158-175` `NetworkBadge` renders "unknown" with the reason only in `title`; `ai/page.tsx:54` puts "Evidence:" and "Risks:" in a `title` on a truncated cell; the approve dialog (110–139) repeats only `reason`.
- Action: render the literal; show evidence and risks in the approve dialog body.

### F17 · P2 · Navigation: dead "unbuilt" branch, missing Evidence entry, anchors into an asynchronously loading page
- Evidence: `ConsoleShell.tsx:36-104` every `GROUPS` item has an `href`, so the `unbuilt` branch (632–646) never renders; console-v2 §2.2 lists Evidence under Scanner Suite with no nav entry; "Markets" → `/settings#markets` (77) and "Users & Security" → `/settings#users` (81) land before the target has height; `/onboarding` is reachable only from the Overview banner and a Settings link.

### F18 · P2 · Consistency: duplicated helpers, divergent tone fallbacks, one hard-coded colour that fails AA in light mode
- Evidence: `fmtBytes`/`fmtDuration` duplicated (`campaigns/page.tsx:28-47`, `system/page.tsx:15-33`); `statusTone` duplicated (`orders/page.tsx:23-28`, `fills/page.tsx:22-27`); book-state tone in three shapes with different unknown-state fallbacks; `notifRoutes`/`notifCooldown` duplicated; `login/page.tsx:64` `hover:text-black` (design-system §1.3 computes 3.97:1 on the light accent). Arithmetic on amounts in components is otherwise limited to pixel geometry and sign checks; F8 is the exception.
- Action: `lib/format.ts`, `lib/tones.ts` (one book-state map, one cycle-outcome map, one chip), a `Notice` component; token colour on the login hover.

### F19 · P3 · Responsive: six-column stat grids at 768 px, unresponsive three-column grids, row actions off-screen on phones
- Evidence: `md:grid-cols-6` beside the 224 px sidebar (`paper/page.tsx:84`, `scanner/page.tsx:187`); `grid-cols-3` unresponsive (`portfolio/page.tsx:46`, `replay/page.tsx:315`); actions in the last column of horizontally scrolling tables (`alerts/page.tsx:70-79`, `screener/page.tsx:514-524`); campaign form labels `w-40 shrink-0` (`campaigns/page.tsx:345`). Nothing breaks outright: the shell collapses, every table has an overflow container, the drawer goes full-screen below md.

### F20 · P3 · Shared-primitive bypasses and request fan-out on Settings
- Evidence: `system/page.tsx:43-65` Process grid gated by readiness with no loading/error branch; `billing/page.tsx:254-256` bare "Loading…"; `onboarding/page.tsx:299-306, 320-323` custom states; Settings mounts seven independent `usePoll(() => api.platform.current(), 15000)` (`components/PlatformSections.tsx:257, 456, 655, 1039, 1503, 1793`) plus capabilities ×4 and two `config.current()`; login collapses every non-429 failure to "Login failed." (`login/page.tsx:24`).

## Checklist cross-reference

| Checklist item | Findings |
|---|---|
| Five-second test | F5 (present: mode, scanner ready, paper running/paused, recorder, alerts, DB; missing: PnL, breakers, feed state, capital total), F1 on mobile |
| Emergency control | F2 |
| Opportunities columns/labels | F3, F10, F14 |
| Triangle/cycle visualisation | Triangle detail has a readable per-leg table with `from → to` and side (`triangles/[id]/page.tsx:40-62`); opportunity-level leg economics are a JSON dump (F3/F10) |
| Active cycles | F4, F6, F7 |
| History | F13 |
| Risk | F12 (breaker states are text + colour, correct) |
| Configuration | F11; the mode switch is clearly restart-scoped |
| UI states | F7, F9, F20; primitives `Await`/`Loading`/`ErrorBox`/`Unavailable`/`Empty` (`ui.tsx:82-185`) and `ScreenerAwait` |
| Responsiveness | F1, F19 |
| Accessibility | F15, F16, F18 |
| Consistency | F4, F8, F14, F18, F20 |
| Workflow friction | F2, F5, F13, F17; PnL split between Portfolio and PnL & Analytics with nothing on Overview; notifications editable in two places |

## Page-by-page

| Page | Purpose | States covered | Key gaps |
|---|---|---|---|
| `/login` | sign-in | busy; 429 copy; generic failure | error not announced (F15); `auth_unconfigured` not distinguished (F20); `hover:text-black` (F18) |
| `/overview` | operations dashboard | per-cell error, per-section `Await`, empties | no PnL/risk/feed state (F5); quick actions are links (F2); no data age (F9) |
| `/scanner` | live stream + ring | loading/error/empty; WS status; `aria-live` | Net bps always green, "Profit" label (F14) |
| `/triangles`, `/triangles/[id]` | topology, quality, legs, cycles | loading/error; empties; engine-absent copy | outcome tones (F4); raw `age_ms` (F9); chips (F15) |
| `/opportunities`, `/opportunities/[id]` | persisted history, economics/decision/simulation | loading/error/empty | F3, F4, F10 |
| `/paper` | engine control, cycles, orders | loading/error/empty; mode-aware empty | F2, F4, F6, F7 |
| `/portfolio` | balances, exposure, session PnL | loading/error/empty | limits never juxtaposed (F12); `grid-cols-3` (F19) |
| `/pnl` | breakdowns, series, distributions | loading/error; rich empties with n | net without asset for non-asset breakdowns; chips (F15) |
| `/orders`, `/fills` | global history | shared primitives; load more | filters (F13); "Latency" without unit |
| `/campaigns`, `/replay` | recorder, campaigns, replays | loading/error/empty; WS; `aria-live` | label widths at 390 (F19); `viewRun` swallows errors (F7) |
| `/ai` | recommendations + analyses | loading/error/empty; approve confirm | evidence/risks in tooltip (F16); reject unconfirmed |
| `/strategies` | versioned config | loading/error; stale-409; field errors; diff confirm | units/defaults/fraction-vs-percent (F11) |
| `/risk` | limits/breakers/events | loading/error/empty | F12; chips (F15) |
| `/alerts` | alert centre | loading/error/empty | actions off-screen on mobile (F19); `text-[10px]` (F15) |
| `/reports` | generate/list/detail | loading/error/empty; RBAC-gated buttons | — |
| `/exchanges`, `/system` | feed counters, books, health | per-section | raw `age_ms` (F9); Process grid can render blank (F20) |
| `/audit`, `/telegram`, `/org` | trail, bot status, members | loading/error/empty | — |
| `/settings` | 12 sections | per-section; stale-409; preview → diff → apply | request fan-out (F20); anchor targets (F17) |
| `/screener`, `/perpetuals`, `/funding`, `/calculator` | Scanner Suite | `ScreenerAwait`; STALE canon; drawer | gross before net (F3); float `/100` (F8); network literal in tooltip (F16) |
| `/scanner-alerts`, `/auto-paper`, `/screener-reports[/id]` | rules, positions, nightly reports | `ScreenerAwait`; confirms; n column with `thin` badge | armed auto-paper rule edits save without diff confirm; `paper_execution_id` not linked |
| `/onboarding`, `/billing` | wizard, subscription | custom states (F20) | float threshold (F8) |

## Verified good (do not regress)

- Money crosses the wire as strings and is rendered verbatim; sign detection is string-based (`ScreenerShared.tsx:134-150`); exact percent↔fraction string shifting exists (297–331).
- Desktop mode banner shows the running mode with configured/pending annotation and degrades to "Mode unknown (backend unreachable)" (`ConsoleShell.tsx:294-346`); `RestartBanner` requires typing RESTART (420–525).
- Paper reset: ADMIN-only, paused-only, type-to-confirm RESET, consequences stated (`paper/page.tsx:125-152, 189-218`).
- `ConfirmDialog` focus-trapped, Escape cancels, click-outside disabled for danger (`ui.tsx:432-516`); strategy apply/rollback show before/after `DiffTable` with effect timing and map backend errors to the field.
- Platform settings: preview → diff → apply with `field_timing` chips, mode-change consequence copy, and "LIVE is not an option" (`PlatformSections.tsx:84-117, 122-206, 425-427`); 409 `stale_version` handled everywhere.
- Scanner Suite stale canon: literal `STALE {age}` text, per-cell dim excluding the age cell (`ScreenerShared.tsx:92-129`); sample size always its own column with a `thin` badge under n < 30 (`RuleEvidenceTable.tsx:116, 171-173`); hypothetical-performance footer and no-transfer note.
- Campaign verdicts render verbatim, never truncated; no verdict → dim, not ok.
- Empty states name an action or a doc, never an env var; `ABSENCE_CODES` gives honest absence copy (`ui.tsx:71-80`).
- WS connection state shown inline; WS-driven regions carry `aria-live="polite"`.
- Tokens only — the single hard-coded colour in TSX is the login hover (F18); focus rings defined for both themes; tabular numerals and numeric alignment.
- Own glyph set; icon buttons carry `aria-label`.
- RBAC mirror includes `reports:generate`; Audit nav role-gated for VIEWER.
- Secrets UI is write-only and clears the value on submit; Billing states "Live execution — Not offered"; paper-only framing is consistent across Auto-Paper, Alert Rules, Settings and Login.
