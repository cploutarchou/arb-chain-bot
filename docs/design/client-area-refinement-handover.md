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
| What needs my attention? | nothing named it; you inferred it from cards | a counted attention list, each item carrying a next action, and the backend's own reason text where one exists (breakers, clock skew, stale settings) |
| Where do I go? | 29 links across six always-expanded groups, plus a duplicate icon rail | six destinations, secondary links only for the one you are in |
| What is "Scanner" versus "Screener"? | you had to already know | each is one sentence of plain description, in the nav and in the breadcrumb |
| What are my results? | per-asset figures with a truncated drawdown | per-asset cards, 2 decimals, exact value a hover away, USDC and USDT never combined |
| Why has nothing qualified? | three counters that invited a wrong subtraction | the scanner's four real stages, each with its own next step, and an explicit note that two of them have no reason breakdown |
| Is this trade feasible? | a green net beside a buried "insufficient liquidity" | the backend's feasibility verdict first, the estimate second, limitations third |
| Where do I change my preferences versus administer the platform? | one continuous form mixing both | three in-page categories by audience (Account, Notifications, Administration — two for an ordinary member), with Organisation and Billing kept as their own routes and platform configuration gated on `platform_admin` |

Measured, not asserted — every figure here is printed by
`web/scripts/gen-route-map.mjs`, so it can be re-derived rather than
trusted: links visible at once fell from **32 to 20 worst case**
(**7** primary + 13 secondary in Operations). Seven, not six, because
Operations is itself one of the primary links — the earlier "6 primary
+ 12" undercounted the rail by one and the worst case by two. Pages that
highlighted no navigation entry fell from **3 to 0**; routes served
stayed at **37**.

On overflow: **77 stops measured, 0 with page-level horizontal overflow,
0 unmeasured**, re-measured against HEAD after every fix below. That is
seven states × five viewports × two themes, **plus 200% zoom on all
seven states**. At 200% the 1440-wide stop renders at 720 CSS px, which
is the *mobile* layout rather than a zoomed desktop one — worth knowing
when reading those five images.

The zoom pass used to skip the two states that need an interaction to
reach, which meant it silently omitted the screener drawer and the
Calculator — and the Calculator is one of the surfaces the original
audit flagged for clipped figures, so it was precisely the one a reader
would assume had been checked. It now runs their `prepare` steps too.

Extending it immediately earned itself, and in a way worth recording:
the drawer then **failed to open** at 200% zoom and at 390×844. The
cause was this change's own doing — `RowDrawer` is deliberately
`role="complementary"` at ≥md and `role="dialog"` below it, and the
capture script was still waiting on the role rather than the drawer's
accessible name. Two of those failures had been present in the previous
run as well, and were reported here as "75 of 75 … zero overflow"
because the reader (me) took the absence of an overflow line for a pass.
**A state that could not be reached is unverified evidence, not clean
evidence**, so `capture-after.mjs` now separates "measured" from
"captured" in its summary and exits non-zero on a prepare failure, which
removes the judgement call from whoever reads the log.

The fifth viewport is **844×390** — a phone in landscape, which is above
the md breakpoint and therefore renders the *desktop* sidebar into 390px
of height. That is the geometry in which the secondary navigation
collapsed to zero height and took every secondary destination with it. A
capture can only show that nothing overflows there; that those entries
are reachable is asserted in e2e, because it is a behavioural claim.

## The reconciliation decision, with the diff already read

Master merged **PR #31 `client-area-navigation`** and **PR #32
`client-area-overview`** after this branch's merge base, so two
implementations of the same two features now exist. Deciding between
them means reading two diffs; this table is that reading. Facts only —
every row is checkable with `git show master:<file>`.

| | master (PR #31) | this branch |
| --- | --- | --- |
| Where the nav lives | inline in `ConsoleShell.tsx`, 788 lines | `lib/nav.ts` (data) + `components/ConsoleNav.tsx` (render); shell is 552 lines |

*Line counts in this table are as measured when the comparison was made,*
*against master's `a8657a7` (still 788) and this branch at `6d5c039`. After*
*the merge the shell is **586** lines — the resolution kept the extracted*
*structure, and the growth is master's non-nav shell content merging in.*
*The split is now `nav.ts` 689 + `ConsoleNav.tsx` 379 beside it.*
| Primary links | 6 destinations | 6 + an explicit **Operations** destination = 7 |
| Selection | route prefixes against stable **destination** ids | same, plus stable ids on **every leaf** (`data-nav-id`, used by the tests) |
| Operator routes | flat `ADMIN_LINKS`, shown when `role === "OPERATOR" \|\| role === "ADMIN"` | entries inside Operations, each gated on the permission the backend actually checks |
| VIEWER and `/risk` | **hidden.** A VIEWER sees no Risk Center, Exchanges, Markets, System Health or Audit Log | visible. `rbac.go` grants VIEWER `PermViewRisk` and `PermViewSystem`, and the brief says not to remove safety access to hit a nav-count target |
| `can()` correctness | `VIEWER` → `perm.startsWith("view:")`, so `can("VIEWER","view:audit")` is **true** while the backend 403s; and `screener:view` does not start with `view:`, so it is **false** where the backend allows it — wrong in both directions | both roles enumerated against `rbac.go`, with a unit test that parses `rbac.go` and asserts agreement in both directions |
| `/settings#markets` in the nav | present in `ADMIN_LINKS`; its own comment notes "usePathname never carries a hash, so /settings#markets matches /settings" — i.e. the same highlight defect this branch fixed, documented as behaviour | fragment-aware resolution: the entry highlights itself and keeps its siblings on screen |
| Entitlement gating in nav | no | `NavAccess` has an `entitlement` kind; gated entries render an honest upgrade note |
| Settings | Organisation, Billing and Telegram as secondary links | in-page categories by audience, nine legacy anchors preserved and focused, plus those routes |
| Tests | none for navigation | 82 unit assertions + 65 e2e, including a 37-route sweep asserting `resolveNav()` against the live DOM |
| Orphan protection | none | `gen-route-map.mjs` exits non-zero if any page resolves to no entry |
| `IconRail.tsx` | deleted | deleted |
| `active=` prop | removed from all 27 pages | made optional with a breadcrumb fallback (no racy 35-file pass) |

Both restructure the navigation to the same shape, so this is not "which
idea is right" — it is which implementation to keep. The two substantive
behavioural differences are the VIEWER safety-access row and the `can()`
row; on both, this branch matches `internal/auth/rbac.go` and master
does not. The cost of keeping this branch is re-resolving
`ConsoleShell.tsx`, `overview/page.tsx` and the `active=` prop on 27
pages against master's versions.

## The merge, as resolved

Master was rewritten again after the table above was written: PRs #30/#31/#32
are no longer separate commits in its history. They were squashed into
`a8657a7` "feat: close T-062 and T-084, and land the client-area refinement",
which moved the merge base from `15f82d2` to `b1faf6d`. Master was merged into
this branch on 2026-09-12. Rescue point: tag `pre-merge-t087`.

**14 files conflicted; 27 auto-merged.** Every conflict was resolved by reading
both sides and asking one question — does master's side hold anything this
branch lacks? — rather than by preferring a side:

| File | Resolution | Evidence the other side lost nothing |
| --- | --- | --- |
| `internal/scanner/scanner_test.go` | both tests kept | master's T-062 `NoViableSize` test and this branch's four-stage partition test both pass against master's `scanner.go`; `go test -race` green |
| `.github/workflows/ci.yml`, `web/package.json` | both runners kept | `npm test` (10, `fmtDecimal`) **and** `npm run test:unit` (82, `presentDecimal`) both run in CI |
| `web/src/lib/decimal.ts` | both APIs kept | `fmtDecimal` and the `presentDecimal` presets coexist; master's 59-line addition is untouched |
| `ConsoleShell.tsx` | this branch's extracted nav | master's inline `DESTINATIONS` holds **31** hrefs; `lib/nav.ts` holds **41**, and set-difference against master's is empty |
| `calculator/page.tsx` | this branch's `ResultPanel` | all nine of master's result fields present, plus `liquidity_unknown`, a third state master lacks |
| `overview/page.tsx` | this branch's attention composer | see below |
| `screener/page.tsx` | this branch's | no API field only on master's side; the two differing names were locals over the same helpers |
| `console.spec.ts` | this branch's | master's one unique test asserts master's wording and its hidden-`/risk` behaviour; the six-destination assertion it carried already exists in the merged file and in `web/unit/nav.spec.ts` |

**One deliberate drop, argued rather than assumed.** Master's Overview reads
`risk.data.reject_reason_counts` and prints the top three rejection reasons
directly beneath a session-scoped evaluation count. That was not grafted in.
The Rejected card on this branch already documents *why* those two figures
cannot be reconciled: `countReject` is reached both from the event consumer
(so rejections dropped before the consumer read them are absent) and from
`OnRevalidationReject`, a later stage reported separately under Engine
internals. Rendering the histogram under the session count re-creates exactly
the reconciliation trap that note exists to prevent. The reasons stay one
click away, behind the card's "See rejection reasons" link to `/risk`, which
is the surface whose scope matches the number. Surfacing the top three
*inside* the Rejected card, labelled "since engine start", would be defensible
— but it is a product change, not a merge resolution, and it is not in this
commit.

Master's two other Overview attention items — "Paper simulation is paused" and
"Persistence not configured" — were dropped as duplicates, not as losses: both
already render in this branch's status strip above the fold, which is where a
*state* belongs. Attention is for anomalies.

**Four defects the merge itself introduced**, all caught by `typecheck`/`lint`
on the merged tree and none by the conflict resolution:

- `NetworkBadge` lost from the `ScreenerShared` import — an import list is the
  one conflict shape where the correct resolution is the **union**, not a side.
- master's `clearFilters` (exact duplicate of `clearAllFilters`), its
  `fmtDecimal` import, its `pathname` read in `ConsoleShell`, and its
  `CATEGORY_ANCHORS` map arrived as orphans once their call sites went.
- master's `function SettingsCategories()` and its `attention` composer
  auto-merged **cleanly** alongside this branch's versions, giving two
  declarations of each. A clean merge is not a verified merge.

A repo-wide scan for duplicate top-level declarations now reports zero.

That scan is structural, so it could not see the two the **e2e suite** caught
after everything static was green:

- `paper/page.tsx` rendered **two** reset controls — master's prominent red
  "Danger zone" `Section` auto-merged alongside this branch's `Collapsible`.
  Removing the panel is the whole point of the change (the audit's note that
  the console's most destructive control had the strongest visual pull), so
  master's copy went. Every safeguard is untouched: ADMIN only, engine paused,
  type-to-confirm, backend-enforced.
- `console.spec.ts:1190` took master's combined assertion
  `getByText(/Excluded: suspect 3/)`. `MetricStrip` renders the label and the
  value as two sibling `<span>`s, so no single element carries that text and
  the assertion cannot pass against this markup. Replaced with
  `/Excluded: suspect\s*3/`, which matches the parent span's concatenated text
  — **stronger** than the branch's previous pair of assertions, because it ties
  the count to its own label instead of asserting a bare "3" somewhere on the
  page.

One further master-side test edit was reverted rather than kept: master renamed
the VIEWER test to "Administration navigation is hidden for a VIEWER". The body
— unchanged, this branch's — asserts the Audit log entry *renders* as a
role-gated, non-navigable control naming its minimum role. The title asserted
the opposite of the test. The accurate name is restored. Master's other spec
edit, scoping "On restart" to `#operating-mode` instead of `.first()`, is a
real strengthening and was kept.

A third failure was **not** a merge regression and was not treated as one.
`every nav page renders content or an honest state` (:81) failed in the suite
and passed alone in 14.9s — the discriminator for this repo. The error was not
an assertion but `route.fetch: Test ended`: the *previous* test leaves a
`/api/v1/system/status` interceptor registered, the status poll keeps firing,
and an in-flight callback rejects after that test ends, which Playwright
attributes to whichever test runs next. Fixed at the source with
`page.unrouteAll({ behavior: "ignoreErrors" })` after that test's assertions,
so no assertion was touched or relaxed.

**The exit code lied once.** `bash scripts/e2e.sh | tail -40` reported exit 0
while two tests failed — the pipeline's status is `tail`'s. The suite result
here is read from `$?` of the unpiped run, and from the explicit
"N passed / M failed" line.

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

Six were pre-existing, two were introduced by this change and caught
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

1. **Master has diverged, and the overlap is this change's own subject.**
   The branch merged master at `15f82d2` (PR #30). Master has since taken
   **PR #31 `client-area-navigation`** and **PR #32
   `client-area-overview`** — a task-based six-destination navigation and
   a priority-first overview, i.e. the same two features as this
   branch's first two commits, implemented differently: master's
   navigation lives inline in `ConsoleShell.tsx` (788 lines), this one
   extracts it into `lib/nav.ts` + `components/ConsoleNav.tsx` (552-line
   shell) with unit tests and a route-map generator. Both delete
   `IconRail.tsx`.

   This is an integration decision, not a mechanical merge, so it has
   **not** been made here: resolving `ConsoleShell.tsx` and
   `overview/page.tsx` by keeping one side necessarily discards the
   other's implementation of the same component. The files that overlap
   are those two plus the `active=` prop on 27 pages. Everything else on
   this branch — `nav.ts`, `ConsoleNav.tsx`, `StatusStrip.tsx`,
   `SettingsCategories.tsx`, `a11y.ts`, the decimal work, the Settings,
   Screener, Calculator, Portfolio and Auto-Paper changes, the tests,
   the generator and these documents — does not exist on master.
2. **The unit suites cover pure logic only.** 92 tests across the two
   runners exercise `decimal.ts`, `nav.ts`, the RBAC matrix against
   `rbac.go`, and the Settings anchor contract. **No component is
   rendered.** The navigation *data* is proven; the navigation
   *component* is not. E2E is what covers that.
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
6. **Independent review found 43 confirmed defects in this branch, and
   they are fixed rather than listed.** A ten-lens adversarial audit
   (each finding then attacked by three refuting lenses; 70 judged, 27
   refuted) found, among others: clicking any Operations › Platform
   configuration entry moved the highlight to Settings and orphaned the
   entry just clicked; every cold `/settings#anchor` deep link activated
   the right tab and then scrolled nowhere; the Administration category
   hid the read-only Scanner Suite view from VIEWER and OPERATOR; a
   platform-only Telegram form sat in a category every member sees;
   Overview blamed a counter gap on dropped events, which cannot cause
   one; the Calculator advised a smaller size where no size could ever
   work; two of eight Auto-Paper columns read fields the backend has
   never sent; and asset balances collapsed sub-cent holdings to
   "< 0.01" while the table below them showed the same quantity in full.

   The mobile drawer and nav sheet modality gap recorded here earlier is
   **now fixed** (`lib/a11y.ts`). What remains open is listed below.

   One deliberate rough edge in that fix: the explanation waits
   `ANCHOR_DEADLINE_MS` (3s) before appearing, because the same constant
   is what stops a *late-appearing* section from pulling focus out from
   under a reader seconds after they followed a link. Three seconds of
   apparent nothing for what is a deterministic permission answer is not
   ideal, and shortening it would reintroduce the focus theft — so it is
   left long on purpose. Anyone tempted to drop it to 300ms should read
   the comment on the focus effect in `SettingsCategories.tsx` first.

   E2E: **68 of 68 pass.** That includes the one long-standing failure
   (`console.spec.ts:134`, a `/strategies` config apply), which is now
   diagnosed and fixed. The first diagnosis — fixture isolation — was
   wrong, and checking it disproved it: no test before that one applies a
   config version. The cause was load; the test five places earlier
   visits every page in the app, leaving `next dev` compiling. It now
   waits on the apply response and asserts its status, which strengthens
   the test rather than relaxing it. See verification.md §3a.
7. **The row-identity and table-naming pass went repo-wide.** 33 tables
   across 24 files now carry a stable `rowKeys` and an accessible
   `label`. Three still key by index, correctly: two are fixed-order
   literal arrays and one is an ordered leg list from a single payload,
   where position *is* the identity. See D10.
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
10. **Parallel work exists and has now landed on master.** See D14 and
    limit 1: what was an uncommitted `client-area-navigation` branch when
    this work started is merged into master as PR #31, with PR #32
    adding the overview half. Reconciliation is the operator's call.

## What was deliberately not changed

No financial formula, risk policy, live-execution restriction, data
retention rule or balance behaviour. No schema migration. No new UI or
charting dependency. No backend behaviour change: the only Go file that
differs is `internal/scanner/scanner_test.go`, which is **test-only** —
it pins the four-stage counter invariant Overview presents, and asserts
which stage each exit path advances. (An earlier version of this
sentence said "zero `.go` files differ from master". That was wrong, and
because this document is read as the summary of the change, the error
propagated: a later independent audit was briefed from it and left that
file unreviewed.) No returned financial value is altered anywhere;
the presentation layer only re-renders exact strings at bounded
precision and always keeps the exact value reachable.
