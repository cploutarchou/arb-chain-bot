# Client-area refinement — handover (T-087)

Branch `t087-client-area-refinement`. Nothing is pushed and no pull
request is open.

Read in this order: this file, then
`client-area-refinement-decisions.md` (what was decided and why, D1–D14),
then `client-area-refinement-verification.md` (what was measured).
`client-area-refinement-routes.md` is generated from the code and lists
every route's new home.

## What became easier

| Question a person arrives with | Before | Now |
| --- | --- | --- |
| Is the platform connected, and is simulation running? | twelve status cards, then six counter cards, then more | one status strip, six states, above the fold |
| What needs my attention? | nothing named it; you inferred it from cards | a counted attention list, each item carrying the backend's own reason and a next action |
| Where do I go? | 29 links across six always-expanded groups, plus a duplicate icon rail | six destinations, secondary links only for the one you are in |
| What is "Scanner" versus "Screener"? | you had to already know | each is one sentence of plain description, in the nav and in the breadcrumb |
| What are my results? | per-asset figures with a truncated drawdown | per-asset cards, 2 decimals, exact value a hover away, USDC and USDT never combined |
| Why has nothing qualified? | three counters that invited a wrong subtraction | the scanner's four real stages, each with its own next step, and an explicit note that two of them have no reason breakdown |
| Is this trade feasible? | a green net beside a buried "insufficient liquidity" | the backend's feasibility verdict first, the estimate second, limitations third |
| Where do I change my preferences versus administer the platform? | one continuous form mixing both | five categories by audience, with platform configuration gated on `platform_admin` |

Measured, not asserted: links visible at once fell from **32 to 18 worst
case** (6 primary + 12 in Operations; 11–13 typical); pages that
highlighted no navigation entry fell from **3 to 0**; routes served
stayed at **37**; and **61 of 61** capture stops across four viewports,
both themes and 200% zoom show zero page-level horizontal overflow.

## What stayed technically distinct — deliberately

The refinement groups these into understandable families. It does not
merge them, because they are not the same thing:

| Pair | Kept apart because |
| --- | --- |
| Triangular scanner vs cross-exchange screener | different engines, different math, different venues-per-opportunity |
| Triangular paper ledger (`/paper`) vs rule-based auto-paper (`/auto-paper`) | different APIs, ledgers, metrics and controls; `internal/screener` has no dependency on `internal/paper` |
| Engine operations reports (`/reports`) vs screener evidence (`/screener-reports`) | different stores, different meaning; one is operational, one is production-gate evidence |
| Organisation administration (`/org`, `/billing`) vs platform administration | a tenant OWNER/ADMIN administers their organisation; only `platform_admin` configures the platform |
| Realized vs marked results | cash basis versus the marked value of what is still held |
| Session vs historical scope | in-memory engine state reset on restart, versus DB-backed windowed queries |

## The correction worth knowing about

The pause control said "Pause paper trading". It reaches only the
**triangular** engine: `internal/screener` has no dependency on
`internal/paper`, and there is **no pause route for Scanner Suite or
rule-based auto-paper at all**. Someone pausing it and believing all
simulated trading had stopped would have been wrong. It now reads "Pause
triangular simulations" and states in visible text — not only a tooltip
— that rule simulations keep running. The dialog adds the two further
facts: cycles already in flight still settle, and the engine is shared
across the deployment rather than scoped to one organisation.

A genuinely global pause needs a backend change. It was not made and is
not requested here.

## Defects found and fixed on the way

Four were pre-existing, three were introduced by this change and caught
by its own verification:

| Defect | Introduced by | How it was caught |
| --- | --- | --- |
| `signTone("0")` returned `"ok"`, so a flat session rendered in the positive colour | pre-existing | UI review of shared code |
| `Table`/`VirtualTable` keyed rows by array index, so a 5 s poll moved focus and selection onto a different record | pre-existing | UI review |
| The horizontal table scroll region was unreachable by keyboard | pre-existing | UI review |
| `RowDrawer` hard-coded a black shadow for both themes — a smear on a white panel in light mode | pre-existing | UI review; `NotificationBell` had a second copy |
| Three pages highlighted no navigation entry because their `active` label no longer existed | pre-existing | reading the shell's selection code |
| The Calculator silently priced the wrong trade when the hand-off venue was outside its six hard-coded options | pre-existing, surfaced by this work | driving the hand-off in a browser |
| `DecimalValue`'s `sr-only` span escaped the table's scroll container and gave the whole page a horizontal scrollbar at ≤768px | **this change** | the capture harness measuring `scrollWidth` |
| The screener's scroll region had no accessible name and no stable row identity | **this change** | the same measurement pass |

## Remaining limits, stated plainly

1. **E2E and independent review are assigned and not yet complete.**
   T-087 is **not** marked DONE. Four assertions in
   `web/e2e/console.spec.ts` are known to need updating (the renamed
   pause control, the Screener opt-ins now behind Advanced, the removed
   Scanner Suite group, and the relocated Audit Log) — the VIEWER
   audit-log assertion must be *moved*, never relaxed.
2. **The unit suites cover pure logic only.** 64 tests across the two
   runners exercise `decimal.ts`, `nav.ts` and the Settings anchor
   contract. **No component is rendered.** The navigation *data* is
   proven; the navigation *component* is not. E2E is what covers that.
3. **The "after" captures come from a disposable in-memory backend**, so
   several states are legitimately emptier than the "before" ones, which
   came from a populated research session. Sound for structure, density,
   responsiveness, theming and overflow; not a like-for-like comparison
   of populated data, and not performance evidence.
4. **No usability study was conducted.** The task walkthrough was
   scripted and driven by one operator (me). Click-path and
   above-the-fold claims are measurements, not findings about real users.
5. **WCAG AA is targeted, not certified.** Contrast is machine-checked
   (54 token-pair assertions). Keyboard, focus-return and the accessible
   roll-up were verified by hand on the audited flows. No screen-reader
   pass and no full audit.
6. **Mobile drawer and mobile nav sheet render a scrim without
   `aria-modal` or focus containment.** Correct as non-modal on desktop;
   on mobile, where they are full-screen with a scrim, modal semantics
   are right. Found, recorded, **not fixed**.
7. **Tables outside the audited flows are keyboard-reachable but
   unnamed.** `Table`'s `label` is optional; the audited surfaces pass
   it, the rest do not yet.
8. **Two open questions for the operator**, neither a security finding
   and neither reopening any existing audit conclusion:
   - `internal/storage/authstore.go` sets `platform_admin = (role ==
     ADMIN)` in both of its writers. Whether provisioning can produce a
     tenant admin with `role=ADMIN` and `platform_admin=false` is not
     settled by the code. If it cannot, tenant organisations cannot
     self-serve Scanner Suite settings today.
   - The two pre-gate scanner stages (`skipped_unhealthy`,
     `no_viable_size`) have counts but no reason dimension anywhere.
     Adding one is a backend change; the console says the breakdown does
     not exist rather than inventing one.
9. **GitHub Actions cannot be used to verify anything.**
   `docs/PENDING.md` §0 records repo-wide failure since 2026-08-31 with
   no runner assigned. "CI-equivalent" here means the local suite.
10. **Parallel work exists.** See D14: master's PR #30 is merged in with
    nothing discarded, but an uncommitted `client-area-navigation` branch
    in the main checkout duplicates the navigation half. Reconciliation
    is the operator's call; `web/e2e/console.spec.ts` is the one file
    both sides must touch.

## What was deliberately not changed

No financial formula, risk policy, live-execution restriction, data
retention rule or balance behaviour. No schema migration. No new UI or
charting dependency. No backend change of any kind — zero `.go` files
differ from master. No returned financial value is altered anywhere;
the presentation layer only re-renders exact strings at bounded
precision and always keeps the exact value reachable.
