---
description: Simplify the client console using the captured UX audit, specialist agents, and verified implementation.
argument-hint: "[optional priorities or constraints]"
disable-model-invocation: true
---

Act as the senior full-stack lead for arb-chain-bot, coordinating UX, UI,
frontend, backend, accessibility, and QA specialists. Implement a substantially
easier-to-use client area in the existing application. This is an implementation
task, not just a critique or a visual reskin. Work through design, implementation,
review, and verification. Do not stop after a plan or after changing the sidebar.

Additional user priorities: $ARGUMENTS

## 1. Read the evidence and establish the baseline

- Read `AGENTS.md`, `CLAUDE.md` if present, and applicable nested instructions.
- Read `.claude/skills/console-feature/SKILL.md`, the relevant portions of
  `.claude/skills/triangular-arbitrage-platform/SKILL.md`, its
  `resources/client-area.md`, and `.claude/skills/scanner-suite/SKILL.md` when
  touching Scanner Suite.
- Read `docs/design/client-area-audit-2026-09-12/README.md` and OPEN its seven
  numbered screenshots. These are a real desktop Brave session, not mockups.
- Read the architecture, master report, roadmap, and handover under `docs/audit/`,
  plus `docs/MASTER_PLAN.md` and `docs/PENDING.md`. T-087 is the existing client
  console reskin task. Reconcile stale backlog entries with code and the latest
  handover; do not reopen resolved financial/security findings by assumption.
- Consult `docs/design/console-ux-audit.md`, `docs/design/ux/console-v2.md`,
  `docs/design/ui/design-system.md`, and `docs/design/scanner-suite.md` as prior
  context. Explicitly supersede old navigation/density requirements where they
  conflict with this requested simplification, while preserving safety rules.
- Inspect git status and preserve all existing user work. Do not commit, push,
  merge, deploy, or change branches containing somebody else's work.
- Inspect the current app, route coverage, tests, auth/entitlement model, API
  contracts, and shared components before editing. Produce a short baseline
  inventory and a current-route-to-new-location map.
- Use the user's connected Brave browser if available. The audited site was
  `http://localhost:3000/overview`, with an already-authenticated ADMIN session.
  Reuse the session; never put credentials in this command, source, screenshots,
  logs, reports, or tests. If authentication is needed and no securely supplied
  credentials are available, request an interactive login.
- Do not assume Codex's browser extension is also available to Claude Code.
  Discover the browser tools actually connected to this session. Report any
  access gap honestly and continue independent code work.

Local runtime note: the audit encountered a Next.js chunk loading failure and a
missing generated `846.js` module. Restarting only the local frontend after
preserving its `.next` cache restored access. The cause was not conclusively
established. Avoid running `next build`, a second dev server, and E2E against the
same `.next` directory concurrently. Use a separate checkout/build directory or
sequence and restart the frontend as necessary. Preserve the existing explicit
`ARB_BACKEND_URL`; never introduce a default proxy destination (T-098).

## 2. Delegate with explicit ownership

Use the project's `.claude/agents/` specialists where available. Read their
definitions first; if an agent is not registered, use an available general agent
with the same bounded responsibility and the relevant definition as context.
Do not stop solely because a specialist alias is unavailable.

1. `ux-designer`: owns the route map, task flows, terminology, progressive
   disclosure, and `docs/design/client-area-refinement-ux.md`. No app code.
2. `ui-designer`: owns the visual/component specification in
   `docs/design/client-area-refinement-ui.md`, grounded in existing tokens.
   No app code. Coordinate with UX before finalizing the layouts.
3. `backend-engineer`: read-only review of endpoint semantics, permissions,
   `platform_admin`, tenant membership, entitlements, data freshness, and the two
   paper/report systems. Identify which proposed summaries are already supported.
   Backend changes require a concrete demonstrated need and a separate assignment.
4. `frontend-engineer`: owns implementation in `web/src/` after the design
   decisions have been reconciled. Avoid multiple writers to `ConsoleShell.tsx`,
   `ui.tsx`, `globals.css`, or shared formatting helpers.
5. `qa-engineer`: owns acceptance coverage in `web/e2e/` and the verification
   record. Test against a dedicated disposable backend, never the user's current
   research session. Coordinate API fixtures and selector changes with frontend.
6. `code-reviewer`: independent read-only final diff review; involve
   `security-engineer` for any permission or tenant-boundary changes.

Tell every worker they are not alone in the repository: do not revert others'
edits, do not edit outside assigned ownership, and coordinate shared-file changes.
Parallelize independent design/contract reviews; integrate code in coherent phases.
Resolve disagreements as lead and document the final decision. Keep concise
progress updates and a checklist so another session can resume.

## 3. Product outcome and design direction

The primary user should quickly answer:

- Is the platform connected, and is paper simulation running or paused?
- What needs my attention, and what can I safely do next?
- Where do I find candidates, understand a candidate, and inspect a simulation?
- What are my simulated results, costs, and evidence limitations?
- Where do I change my own preferences versus administer the platform?

Keep the product's existing dark/light identity and tokens. Prefer a calm,
readable workspace with clear headings, descriptive labels, compact summaries,
and detailed data on demand. No marketing hero, decorative gradients, new brand,
unnecessary animation, or oversized cards that push the working data down.

Use the following as the initial IA; adjust only with a documented task-based
reason, not merely personal taste. Preserve every currently supported route.

| Primary destination | Existing surfaces presented contextually |
| --- | --- |
| Overview | `/overview`; priority status, attention items, simulation summary |
| Discover | `/screener` cross-exchange; `/scanner` triangular; `/perpetuals`; `/funding`; contextual `/calculator`, `/triangles`, `/opportunities` and detail pages |
| Paper Trading | `/paper` triangular simulations; `/auto-paper` rule simulations; contextual `/portfolio`, `/orders`, `/fills`, `/cycles/[id]` |
| Research & Results | `/pnl`, `/screener-reports`, `/reports`, `/campaigns`, `/replay`, `/ai`, with explicit source labels |
| Alerts & Rules | `/alerts`, `/scanner-alerts`, `/strategies`; keep incident alerts distinct from rule configuration |
| Settings | Account preferences, organisation, billing, notifications; platform administration only for entitled operator staff |

Keep risk status and the risk destination immediately discoverable from Overview
and Paper Trading. Put `/risk`, `/exchanges`, `/system`, `/audit`, and platform
configuration under an explicitly labelled operator administration area where
appropriate. Do not remove safety access to satisfy a navigation-count target.
Organisation administration is not platform administration. `/org`, `/billing`,
`/telegram`, `/onboarding`, all Settings anchors, and all detail routes must remain
reachable with their existing authorization semantics.

## 4. Implement the complete refinement

### A. Shell and navigation

- Replace the default wall of 29 group links plus three footer links with about
  six primary choices and contextual secondary navigation. Remove the redundant
  simultaneous group icon rail unless user testing shows a clear need for it.
- Use stable navigation IDs and route/segment matching. The current shell uses
  display labels for active groups/icons; renaming text must not break selection.
- Use one navigation definition for desktop/mobile, role-aware visibility,
  deep-link handling, accessible current-page state, and contextual breadcrumbs.
- Keep existing URLs/bookmarks/back-forward behavior; do not force a route rewrite
  to get a simpler navigation structure. Tests that assume all 29 links are
  simultaneously visible should assert discoverability through the new structure.
- Never flash privileged navigation while auth is loading. Retain honest
  unavailable/upgrade explanations for entitlement-gated features where useful.
- Preserve a persistent paper-only mode indicator and clearly scoped pause/resume
  controls at every width. Verify the control's actual engine scope: do not imply
  the triangular pause control also pauses Scanner Suite if the backend does not.
- Make sidebar and account actions legible without nested horizontal scrolling.

### B. Overview: priority first

- Put connection/mode, simulation state, attention items, and a clear next action
  above the fold. Consolidate duplicate status cards into a readable status strip.
- Show a small number of primary summaries: simulated net results, drawdown,
  active work, and items needing attention. Keep fees and asset units explicit.
- Keep USDC and USDT figures separate; never silently sum currencies. Preserve
  session versus historical scope and realized versus marked distinctions.
- Move frames, resyncs, internal counters, and detailed diagnostics into labelled
  secondary sections or their existing diagnostic pages. Retain observability.
- Explain zero qualified candidates using actual available reasons. Do not infer
  that detected minus rejected equals qualified: counters can represent different
  stages or intervals. Never invent a rejection cause or computed summary.
- Give empty sections a precise explanation and a relevant navigation action.
  Existing valid warnings, campaign verdicts, and evidence limitations stay visible.

### C. Discovery tables and filters

- Clearly distinguish cross-exchange screening from triangular opportunities.
  Avoid making users understand “Scanner” versus “Screener” without explanation.
- Show common filters first: asset/pair, exchange selection, quote asset, and an
  accurately named threshold. Keep gross/net semantics identical to the API.
- Move allow/deny lists, lifetime, optional fee overrides, and unsafe-lane opt-ins
  into an Advanced section. Preserve current safe defaults and saved templates.
- Show applied-filter chips/count and an obvious clear action. Persist harmless
  preferences using existing mechanisms; namespace by user/org when appropriate.
- Reduce the default table to decision-relevant columns. Keep pair, both venues,
  estimated spread after applicable costs, liquidity, freshness/limitations, and
  a detail action visible. Expose the full existing information in details or
  optional columns, with row identity and keyboard access preserved.
- Correct overflowing raw decimal display centrally using `web/src/lib/decimal.ts`
  and `web/src/lib/format.ts`. Use bounded presentation precision, separators,
  aligned tabular numerals, and explicit units. Preserve the raw decimal string
  for exact values, sorting, filters, forms, exports, and API requests.
- Never use `parseFloat`, `Number`, or `toFixed` as a shortcut for money decisions
  or persisted values. Tiny nonzero values must not appear as a true zero; retain
  sign with a suitable less-than display and an accessible exact-value disclosure.
  Do not show a minus-zero artifact. Test large and tiny decimal values.
- Preserve virtualization and bounded subscriptions. Background updates must not
  steal focus or silently change the selected row. When a candidate disappears,
  retain context and show that it is unavailable/stale rather than another row.
- Keep financial, freshness, network-unknown, and safety caveats alongside the
  relevant value; do not bury them beneath a long scrolling table.

### D. Detail and Calculator flow

- Preserve the working Screener → Detail → Calculator prefilled handoff.
- Make the drawer fit its viewport without horizontal scrolling; keep its title,
  age/status, exact-value disclosure, and action footer usable. Maintain Escape,
  focus containment where modal, labelled controls, and focus return on close.
- Calculator: basic pair/venues/size first; overrides under Advanced. Use quote
  asset units, field help, inline validation, and a clearly scoped Calculate CTA.
- Present feasibility before the estimate. The audit showed green net values
  alongside “insufficient” liquidity. Promote the backend liquidity/stale/guard
  outcome into a prominent explanation, then show the estimate and limitations.
  Do not invent qualification, change returned numbers, or recalculate finance.
- Provide a return link that restores discovery filters/selection where possible.

### E. Simulation and results

- Bring triangular paper monitoring and rule-based automatic paper monitoring
  into one understandable navigation family, with clearly named subviews. Keep
  their APIs, ledgers, metrics, controls, and result meanings distinct.
- Prioritize running state, active positions/cycles, results and explanations.
  Move session reset below normal work into an advanced administrative section;
  preserve pause prerequisite, RBAC, CSRF, confirmation, and backend safeguards.
- Replace implementation jargon such as “persisted cycles”, “in-memory ring”,
  and raw internal IDs in primary labels with readable task language. Retain
  technical detail in expandable sections and exact identifiers for inspection.
- “No cycles yet” should not imply a missing database when the database is healthy.
  Separate healthy empty results, loading, service errors, permission denial,
  and unavailable configuration based on actual response/state information.
- Add contextual links from Auto-Paper to relevant rules and evidence. Prefer a
  rule name where the API supplies one; otherwise keep a labelled short identifier
  with full-value access rather than fabricate a human name.
- Keep engine operations reports and screener evidence clearly differentiated.
  Expose sample size, observation window, costs, and backend verdicts unchanged.

### F. Settings

- Replace the continuous mixed-purpose settings form with focused categories:
  Account, Organisation, Billing, Notifications, and permitted Administration.
- Within Administration, group operating mode, markets/assets, venue/fee settings,
  Scanner Suite configuration, AI settings, logging, versions, users, and vault
  access according to existing permissions. Render only the active category's
  heavy content where feasible without losing unsaved edits or subscriptions.
- Preserve old anchors including `#markets`, `#users`, `#security`,
  `#scanner-suite`, `#operating-mode`, `#logging`, `#ai`, `#platform-versions`,
  and `#notifications`: direct links must activate and focus the right category.
- Use edit → preview diff → confirm → apply → feedback. Preserve `parent_version`,
  stale-version conflict handling, rollback/history, backend timing chips, and
  restart-required meaning. Warn before discarding unsaved edits.
- Replace task IDs, implementation names, and backend-wiring explanations in
  ordinary helper text with plain language. Preserve backend error messages and
  risk/verdict text verbatim, with supplementary explanation rather than rewriting.
- Never expose platform settings to ordinary tenant admins based only on ADMIN
  display role. Check `platform_admin`, permission checks, and entitlements.

## 5. Implementation seams and scope limits

Build on `web/src/components/ConsoleShell.tsx`, `IconRail.tsx`, `ui.tsx`,
`FilterCard.tsx`, `RowDrawer.tsx`, `PaperControl.tsx`, `RuleEvidenceTable.tsx`,
`GatedControl.tsx`, `PlatformSections.tsx`, and `ScreenerSettingsSection.tsx`.
Reuse `web/src/app/globals.css` tokens and `web/src/lib/{auth,decimal,format,usePoll,ws}`
plus the typed client in `web/src/lib/api/client.ts`.

Prefer small focused components over an expanding shell/page monolith. Do not add
a UI framework or charting dependency unless an existing capability is genuinely
insufficient and the tradeoff is recorded. Do not change financial formulas,
risk policy, live-execution restrictions, data retention, or balance behavior.
Do not fabricate telemetry or add frontend-only financial aggregates.

Backend changes, if truly necessary for a confirmed UX blocker, must remain narrow:
typed contracts, tenant scope from context, backend RBAC/CSRF, structured error
envelopes, request caps, audited mutations, and appropriate tests. Use existing
settings documents. A UI reorganization should not require a schema migration.

## 6. Verification and acceptance

Create a before/after verification report, not a claim based only on screenshots.

- Capture the same seven audit states before/after at the same desktop viewport.
  Also verify at 1440×900, 1024×768, 768×1024, and 390×844, both themes, and 200%
  zoom. No page-wide horizontal overflow; complex tables may scroll inside an
  explicit accessible region. Primary actions remain reachable.
- Measure the primary-navigation count and click path to every existing route.
  The six primary destinations fit without scrolling at 1440×900; secondary
  routes are reachable within two navigation activations where practical.
- Overview shows mode, health, simulation state, attention count and the next
  action without scrolling at 1440×900. Verify with a scripted task walkthrough;
  do not claim a real user usability study was conducted.
- Verify direct URLs, anchors, active navigation, back/forward, and role changes.
  Use isolated fixtures for VIEWER, OPERATOR, tenant ADMIN/OWNER, platform staff,
  and restricted entitlements. No tenant data crosses boundaries.
- Test loading, empty, stale, failed requests, 401, 403, and stale-setting-version
  responses. An error never appears as zero results or successful submission.
- Test decimal display independently of raw data: huge values, tiny signed values,
  exact zero, nullable/invalid values, asset units, unchanged sorting/API payloads,
  and access to full precision. Test virtualized row identity/focus during refresh.
- Test keyboard-only navigation, visible focus, drawer Escape/focus return,
  category/tab semantics, labels, error announcements, reduced motion, and contrast.
  Target WCAG AA; do not claim compliance from screenshots alone.
- Confirm settings preview/apply/cancel/conflict, mobile navigation, preserved
  Calculator handoff, and correctly scoped paper pause/resume in disposable E2E.
  Never exercise reset, trading controls, account mutations, or settings writes
  against the currently running research backend at port 8080.
- Run `npm run lint && npm run typecheck && npm run build` under `web/` and the
  repository's `scripts/e2e.sh` against its dedicated backend. If tests rely on
  real database behavior, use a disposable test database as instructed by AGENTS.
  Sequence builds and dev/test servers to avoid shared `.next` corruption.
- Run the applicable repository checks: `gofmt -l internal cmd`, `go build ./...`,
  `go vet ./...`, `golangci-lint run ./...`, and `go test -race -count=1` for
  changed Go packages; complete the broader required CI-equivalent suite before
  declaring the change done. Record skips/environment blocks as unverified.
- Independent reviewer checks the complete diff and tests. Fix blocking findings;
  do not weaken assertions or financial safeguards to get green checks.

## 7. Deliverables and finish

Deliver working code, the UX and UI specifications, the complete old/new route
map, before/after screenshots, test evidence, and a concise handover explaining
what became easier, what stayed technically distinct, and any remaining limits.
Update T-087 and relevant design/operator documentation honestly; mark DONE only
when acceptance and required checks pass. Do not change unrelated historical
audit conclusions. Keep all work uncommitted unless the user explicitly asks.

Make reasonable design decisions and continue; do not ask the user to approve
every layout, label, or component. Ask only when a real missing input or a
consequential action outside this scope requires it. Do not enable live trading,
transfer funds, rotate credentials, change actual research settings, or publish.
