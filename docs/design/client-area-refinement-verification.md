# Client-area refinement — verification record (T-087)

Measured evidence for the refinement, kept separate from the design
documents so a reviewer can check claims without reading specs. Anything
not actually run is recorded as **unverified** with the reason, never as
passing.

Before-state evidence: `client-area-audit-2026-09-12/` — seven native
desktop captures from a real Brave session on 2026-09-12. These are
**not** recaptured: the README records that they are captures of changing
live data whose values differ between runs, so re-shooting them would
produce a different "before", not a better one.

## 0. Re-verification after the master merge (2026-09-12, 23:xx)

Everything in §1 onward was first measured at `6d5c039`, **before** master's
`a8657a7` (T-062, T-084 and master's own client-area variant, ~50 files) was
merged in. A clean merge is not a verified merge, so the whole suite was re-run
on the merged tree. These are the merged-tree numbers:

| Check | Result on the merged tree |
| --- | --- |
| `gofmt -l internal cmd` | clean |
| `go build ./...` / `go vet ./...` | pass |
| `golangci-lint run ./...` | **0 issues** |
| `go test -race -count=1 ./internal/scanner/ ./internal/storage/` | pass — master's T-062 `NoViableSize` test and this branch's stage-partition test both green against master's `scanner.go` |
| `npm run lint` (eslint + contrast) | clean, 54/54 contrast checks |
| `npm run typecheck` | clean |
| `npm run build` | pass |
| `npm test` (node:test, `fmtDecimal`) | **10/10** |
| `npm run test:unit` (Playwright, `presentDecimal`/nav/rbac/anchors) | **82/82** |
| `scripts/e2e.sh` | **68/68, exit 0** |
| `node scripts/gen-route-map.mjs` | 37 routes, **0 orphans**, 38 standing entries |
| duplicate top-level declarations across `web/src` + `web/e2e` | **0** |

Test counts are unchanged from the pre-merge branch (68 e2e, 82 unit, 10
node:test), and both decimal test runners survive the merge — that was the
point of keeping both `web/package.json` scripts and both CI steps.

Three failures surfaced on the merged tree and all three were fixed at the
source, not by relaxing an assertion; they are described in the handover's
"The merge, as resolved". One caution recorded for whoever runs this next:
`bash scripts/e2e.sh | tail` reports the **pipe's** exit status, not the
suite's. An early run showed "exit 0" with two tests failing. Read `$?` from
the unpiped command, or the explicit "N passed / M failed" line.

The capture sweep was re-run too, and it needed to be: the merge removed
master's duplicate renderings from `/paper` (the `Danger zone` panel),
`/overview` (the second attention composer) and `/settings` (the local
`SettingsCategories`) — three of the seven audited states. "No layout
primitive changed" was reasoning, not measurement, and by then it was no
longer even true.

```
77 stops MEASURED, 0 with page-level horizontal overflow, 0 NOT MEASURED (prepare failed)
```

Same matrix as before the merge: seven workflows × {1440×900, 1024×768,
768×1024, 390×844, 844×390} × both themes, plus a 200% zoom pass over all
seven. `capture-after.mjs` exits non-zero on a prepare failure, so
"0 NOT MEASURED" is the script's own assertion, not a reading of the log.

## 1. Repository checks

| Check | Command | Result |
| --- | --- | --- |
| Go formatting | `gofmt -l internal cmd` | clean (no output) |
| Go build | `go build ./...` | pass |
| Go vet | `go vet ./...` | pass |
| Go tests for changed packages | `go test -race -count=1 ./internal/scanner/` | pass. One **test-only** Go file changed: `internal/scanner/scanner_test.go` gains `TestEvaluationStatsPartitionEveryTriangle`, which asserts the counter identity Overview's four-stage explanation depends on. No non-test Go file differs from master. |
| Console lint | `cd web && npm run lint` | pass (ESLint clean + 54/54 contrast assertions) |
| Console typecheck | `cd web && npm run typecheck` | pass |
| Console build | `cd web && npm run build` | pass — clean `rm -rf .next` build on the merged tree, all 37 routes |
| Console unit suite (this branch) | `cd web && npm run test:unit` | pass — 82 tests |
| Console unit suite (master's, PR #30) | `cd web && npm test` | pass — 10 tests |
| `golangci-lint run ./...` | `golangci-lint run ./...` | pass — **0 issues** (v2.13.1) on the merged tree |
| Playwright e2e | `scripts/e2e.sh` | **68 of 68 pass** (`EXIT=0`). The one long-standing failure is diagnosed and fixed — see §3a |
| GitHub Actions CI | — | **unverified, environment blocked.** `docs/PENDING.md` §0 records repo-wide Actions failure since 2026-08-31 with no runner assigned and no logs, on every branch including `master`. "CI-equivalent" here means the local suite above. |

**On the Go row.** No `.go` file is modified by this branch's own
commits; the merge brought master's Go changes in, and gofmt, build and
vet were re-run on the merged tree and are clean. The three checks are
recorded as run rather than skipped, so "no Go changed" is proved
instead of asserted.

**On the two unit suites.** Master's PR #30 landed a `node --test`
runner over `src/lib/*.test.ts` with a CI step that invokes it. This
branch's suite uses the Playwright runner already in devDependencies.
Both are kept and both pass; the merge commit explains why neither was
collapsed into the other.

### What the unit suite does and does not cover

82 tests pass, and it is worth being exact about what that means: they
cover **pure logic** — `lib/decimal.ts`'s presentation rules,
`lib/nav.ts`'s route resolution and access metadata, `lib/auth.tsx`'s
permission matrix checked against `internal/auth/rbac.go`, and the
Settings page's anchor set read out of its source. **Nothing renders a
component.** `ConsoleNav`, `StatusStrip`, `SettingsCategories` and the
rewritten pages have no unit coverage; the navigation *data* is proven,
the navigation *component* is not. The e2e suite is what exercises the
shell in a browser, and the responsive, keyboard and focus checks below
are what exercise the rest. "82 passed" should not be read as component
coverage.

### Unit suite added by this change

`web/` had no unit-test runner at all before this task — only ESLint, a
contrast script and Playwright e2e. Rather than add a test framework, the
suite reuses the `@playwright/test` runner already in
`devDependencies`, under its own config with no `webServer` and no
browser, so it needs neither Next nor a backend:

```bash
cd web && npm run test:unit     # playwright test -c playwright.unit.config.ts
```

## 2. Navigation: measured before and after

Generated, not estimated: `web/scripts/gen-route-map.mjs` reads
`web/src/lib/nav.ts` and scans `web/src/app` for pages, writes
`client-area-refinement-routes.md`, and **exits non-zero if any page
resolves to no navigation entry**. That exit code is what makes "every
route is preserved" a checked claim.

```
$ cd web && node scripts/gen-route-map.mjs
ok: 37 routes mapped, 0 orphans, 38 standing entries
```

| | Before | After |
| --- | --- | --- |
| Primary choices | 6 groups, all expanded by default | **7** primary links (6 product destinations + the operator area, which is itself a primary link) |
| Links on screen at once | 29 group links + 3 pinned = **32** | **20 worst case** (7 primary + 13 secondary, in Operations); 9–14 typical |
| Duplicate group control (icon rail) | present, alongside the label column | removed |
| Pages served | 37 | 37 — unchanged |
| Pages highlighting no nav entry | **3** | **0** |
| Selection keyed on | display label string | stable id + route matching |

Secondary entries per destination: Overview 1, Discover 7, Paper Trading
5, Research & Results 6, Alerts & Rules 4, Settings 2, Operations 13.
(Settings is 2, not 4: `settings-home` is contextual and only
Organisation and Billing are standing entries.)

The three pages that previously highlighted nothing — `/cycles/[id]`
(passed `"Paper"`, label was `"Paper Trading"`), `/screener-reports/*`
(passed `"Screener Reports"`, label was `"Evidence — Screener Reports"`)
and `/onboarding` (no such label) — now resolve, asserted individually
in `web/unit/nav.spec.ts`.

### Route preservation is enforced, not asserted

`web/unit/nav.spec.ts` walks `web/src/app` on disk, substitutes a
concrete id into every dynamic segment, and fails with the offending
paths listed if any page resolves to no navigation entry. It cannot
drift from the app, because it reads the app.

## 3. Decimal presentation

`web/unit/decimal.present.spec.ts` — the cases the brief requires, each
also asserting the exact backend string survives untouched.

| Case | Assertion |
| --- | --- |
| The audit's real 27-digit value | `1078.65168539325842696629213` → `+1,078.65 bps`, `exact` unchanged, `rounded` true |
| Rounding is not done through a float | a 30-digit integer with a fractional tail renders every integer digit; `Number(x).toFixed(2)` would have lost them before rounding |
| Carry propagation | `9.999` → `10.00`; `999999.999` → `1,000,000` |
| Exactness not overstated | `12.3400000` at 2 dp is **not** flagged rounded — the dropped tail is zeros |
| Tiny positive | `0.0000000012` → `+<0.01`, `tiny` true, `zero` false |
| Tiny negative | `-0.0000000012` → `-<0.01 USDC`, sign kept, never `0.00` |
| No minus zero | `-0`, `-0.0`, `-0.000`, `-0E-9` all → `0.00 USDT`, unsigned |
| Exact zero ≠ tiny | `0` → `0.00 USDC`, `zero` true, `tiny` false |
| Unknown ≠ zero | `null`, `undefined`, `""`, `"-"`, `"."`, `"abc"`, `"1.2.3"`, `"NaN"`, `"Infinity"` → `—`, `valid` false, `exact` null |
| Asset units never merged | same figure in USDC and USDT renders as two different strings |
| Small prices keep meaning | `0.00224` → `0.00224`, not `<0.01` (significant-digit mode below 1) |
| Sorting unaffected by display | two values differing past float precision render identically yet still order correctly via `cmpDecimalStr` |
| No re-encoding | `"0.010000"` displays as `0.01 USDC` with `exact` still `"0.010000"` |
| Zero is not a gain | `signTone` returns `dim` for every zero spelling — this returned `ok` before, so a flat session rendered green |

### Settings anchors: the page is checked, not just the metadata

`web/unit/settings-anchors.spec.ts` reads `app/settings/page.tsx` and
asserts its rendered `<SettingsAnchor anchor="…">` set matches
`SETTINGS_SECTIONS` in **both** directions — nothing declared but
unrendered, nothing rendered but undeclared (an undeclared anchor cannot
map to a category, so a deep link to it would land on the default tab).
It also asserts all nine pre-existing anchors are present, that the four
linked from inside the console are present, and that no section anchor
is a bare `<div id>` — the wrapper is what carries `tabIndex={-1}`, and
without it a deep link would scroll without moving focus.

This closes a real gap: the earlier test proved only that the metadata
table *claimed* nine anchors, which the page could have contradicted.

## 3c. Screener filter URL sync (D11)

All ten filters round-trip. Verified in e2e
(`screener filters round-trip through the URL, and an opt-in is never
enabled by a malformed link`), which asserts four separate things rather
than only that the feature exists:

| Assertion | Why it is the one worth making |
| --- | --- |
| `min_spread_bps=42` appears in the URL after typing | the common-filter half of the sync |
| `min_liquidity=1234.56789` appears **as that string** | money must not round-trip through a float; a numeric parse here would be invisible until it lost a digit |
| reload restores both values into their fields | the point of the feature — bookmark, share, Back |
| `?include_suspect=yes-please` leaves the box **unchecked**, `?include_suspect=1` checks it | the safety-relevant case: these opt-ins re-admit lanes the backend excludes by default, so a malformed link must fall back to the safe default rather than to "on" |
| navigating to a different `?min_spread_bps=` adopts the new value | Back, Forward and the Calculator hand-off all depend on an external URL change beating the component's mount-time state |
| "Clear filters" empties the query string | otherwise a reload silently restores what the operator just cleared |

## 3d. Row identity across every table that can reorder (D10)

55 tables in 31 files now pass `rowKeys` and `label`. The mechanical
check that none was missed:

```
$ for f in $(grep -rl "<Table\b\|<VirtualTable\b" app components); do
    tot=$(grep -c "<Table\b\|<VirtualTable\b" "$f"); keyed=$(grep -c "rowKeys=" "$f")
    [ "$tot" -gt "$keyed" ] && printf "%s %s/%s\n" "$f" "$keyed" "$tot"
  done
app/screener-reports/[id]/page.tsx   0/1
app/opportunities/[id]/page.tsx      0/1
components/ui.tsx                    1/2
```

Those three are deliberate: two are fixed-order literal arrays and one is
an ordered leg list from a single payload, where the array position *is*
the row's identity; `ui.tsx` is the component's own internals. Keying
them by a fabricated composite would be worse, not better.

## 3a. The one failing e2e test — diagnosed, then fixed

`console.spec.ts` — *structured config edit applies as a new version end to end* — *structured config edit applies as a new version
end to end* — failed inside the full suite while passing in isolation in
2.1s. It was pre-existing: it failed once for the QA pass too, on a tree
without this branch's fixes, and `/strategies` is not in this branch's
diff.

The first diagnosis here was wrong in an instructive way. It said the
cause was fixture isolation — several tests applying versions of one
shared versioned document, leaving a stale `parent_version`. Checking
that claim disproved it: **no test before this one applies a config
version at all.** The real cause is load. The test five places earlier
(`every nav page renders content or an honest state`) visits every page
in the app, which leaves `next dev` compiling routes on demand, so the
apply round trip can exceed a fixed 10 s wait.

Fixed by waiting on the POST to `/api/v1/config` and asserting its
status before asserting the confirmation text. That **strengthens** the
test rather than relaxing it — the request must now actually be made and
actually succeed, where before only a rendered string was required. The
`Version \d+ active.` assertion is untouched; it is the only coverage of
the edit → preview diff → confirm → apply → feedback loop completing,
and loosening it to get green would have deleted exactly that.

Generalisable for this suite: "fails in the suite, passes alone" is more
often `next dev` compile pressure than shared state. Wait on the network
response, not on a clock.

## 3b. Float-shortcut audit across `web/src`

Grepped for `parseFloat`, `toFixed` and `Number(` outside tests.
**Nothing this change introduces is a float shortcut on a money value.**

An earlier version of this section claimed to have "classified every
hit" and then listed two. It had not: an independent audit found four
more, all pre-existing, and two of them are the same defect class as the
drawdown P0 this branch fixed —

| Location | Hit | Status |
| --- | --- | --- |
| `app/risk/page.tsx:272` | `Number(limits["max_drawdown"]) * 100` for a limit label | **fixed here** — `presentPercentFromFraction` |
| `app/risk/page.tsx:285` | `(Number(a.drawdown) * 100).toFixed(1)` for a drawdown | **fixed here** — same |
| `lib/strategyFields.ts:320,336` | `Number(trimmed)` validating a bps and a quote-money threshold | pre-existing, untouched: validation bounds only, the exact string is what is submitted |
| `app/replay/page.tsx:116,232,381` | `Number()` on a speed multiplier and a config version | pre-existing, untouched: neither is money |
| `lib/format.ts:39`, `app/system/page.tsx` | `toFixed` on byte counts, GC pauses, latencies, msgs/sec | pre-existing, untouched: none is money |

The two risk-page fixes are display-only — a `Stat` label and a summary
string, neither a form value — so no submitted payload changes.

| Hit | Verdict |
| --- | --- |
| `lib/decimal.ts:270` `Number(expRaw)` | the **integer exponent** of a decimal string (`1.5e3` → `3`), bounded by an explicit `Math.abs(exp) > 10000` guard and used only to shift the point on the digit string. Not a value. |
| `lib/format.ts`, `lib/feedState.ts` `toFixed` | byte counts and elapsed seconds. Not money. |
| `app/onboarding`, `app/settings`, `app/scanner-alerts` `Number(cooldownS)` etc. | integer configuration fields (seconds, hours) whose API types are numbers. Legitimate. |

Two **pre-existing** hits are on value-ish paths, were not introduced
here, and are **left unfixed as out of scope** — recorded so they are
not mistaken for clean:

| Location | What it does |
| --- | --- |
| `app/billing/page.tsx:184` | `(Number(line.payout.refund_rate_90d) * 100).toFixed(1)` — renders a backend decimal rate as a percentage through a float |
| `app/triangles/[id]/page.tsx:72,75` | `Number(leg.fee_rate ?? 0)` for a comparison and a `Math.max` across legs |

Neither is a ledger or a persisted value, and neither is on a surface
this task was asked to change. They are the obvious next candidates if
the pattern is to be eliminated repo-wide.

## 4. Defects found by review and fixed

| Defect | Location | Evidence it is fixed |
| --- | --- | --- |
| `signTone("0")` returned `"ok"`, rendering an exactly-zero result in the positive colour | `lib/decimal.ts` | 5 regression tests incl. `-0`, `0e-9`, and a tiny nonzero loss |
| `Table`/`VirtualTable` keyed rows by array index, so a 5 s poll or a re-sort reused a DOM row for a different record and moved focus/selection silently | `components/ui.tsx` | optional `rowKeys` threaded through both branches; call sites updated on the tables this change touches |
| The `overflow-x-auto` table wrapper had no `tabIndex`/`role`/`aria-label`, so columns starting past the right edge were unreachable by keyboard (WCAG 2.1.1) | `components/ui.tsx` | focusable scroll region with an optional accessible name |
| `RowDrawer` hard-coded `rgba(0,0,0,.25)` for both themes — a black smear on a white panel in light mode | `components/RowDrawer.tsx` | `--shadow-color` token added to all three token blocks; contrast script still 54/54 |

## 5. Pause-control scope

The control claimed a scope it did not have. Verified against the Go
source and corrected; see `client-area-refinement-backend-contract.md` §1
for the file:line evidence.

| | Before | After |
| --- | --- | --- |
| Button | "Pause paper trading" | "Pause triangular simulations" |
| Stated scope | none | visible text: rule-based automatic paper execution keeps running and has no pause control |
| Dialog | in-flight cycles settle | in-flight cycles settle **+** triangular engine only **+** shared across the deployment, not per organisation |

## 6. Responsive, theme and zoom matrix — measured

`web/scripts/capture-after.mjs` drives the seven audited workflows
across the acceptance matrix and records
`documentElement.scrollWidth` against `clientWidth` at every stop.
Screenshots land in `client-area-refinement-after/`; the numbers land in
`client-area-refinement-after/overflow-report.json`. The script exits
non-zero if any stop has page-level horizontal overflow.

```
61 stops captured, 0 with page-level horizontal overflow, 0 prepare errors
(the first run; see the re-measured 77-stop table below)
```

| Dimension | Covered |
| --- | --- |
| States | the same seven the audit captured, in its order |
| Viewports | 1440×900, 1024×768, 768×1024, 390×844 |
| Themes | dark and light (emulated `prefers-color-scheme`, with the stored theme choice cleared so the media query is authoritative) |
| Zoom | 200%, emulated as a halved CSS viewport rather than a scaled image — on **5 of the 7 states**. The zoom pass skips states needing an interaction to reach, so the screener drawer and the Calculator are not covered; and at 720 CSS px what renders is the **mobile** layout, not a zoomed desktop one |

The measurement is the point: a clipped page and a fitting page look
identical in a viewport-sized screenshot. Which is why the first run
mattered — it failed.

### What the first run found

**Eight of 61 stops overflowed** in the first run, all on the screener at 768px and
below, and the cause was in this change:

`DecimalValue` renders the exact value in a `sr-only` sibling.
Tailwind's `sr-only` is `position: absolute`, so with no positioned
ancestor the hidden text resolves against the **initial containing
block** at its static position — which, inside a horizontally scrolled
table, is far to the right of the viewport. Because the scroll container
was not its containing block, the scroller did not clip it, and it
extended `documentElement`'s scrollable overflow. Measured: `html`
scrollWidth 677 against a body and viewport of 390.

Adding `relative` to the `DecimalValue` wrapper makes it the containing
block. All 61 stops then measured zero, and the re-run below measures zero across all 77.

Two further defects surfaced in the same pass:

| Defect | Consequence | Fix |
| --- | --- | --- |
| The screener's spread table had no accessible name on its scroll region | at ≤1024px the seven columns need internal scrolling; the region was focusable but unnamed, so a screen-reader user tabbing into it learned nothing | `label="Cross-exchange spreads"` |
| The same table had no stable row identity | React keyed rows by array index, so a 5 s poll that reordered rows reused a DOM row for a different pair, moving focus and the open drawer's anchor onto a candidate the user never selected | `rowKeys` from the existing `rowKey(r)` |
| The Calculator's venue `<select>` offered six hard-coded names while the screener covers every venue the status endpoint reports | a `<select>` whose value is absent from its options renders the **first** option, so arriving from a `binance → kucoin` row showed `binance → binance` and Calculate would have priced a different trade than the row clicked | options sourced from `/screener/status`, unioned with whatever the hand-off asked for |

### Verified by hand in a browser

Against the disposable backend, not the research session:

| Behaviour | Result |
| --- | --- |
| Drawer opens → focus moves to the close button | pass |
| Escape closes the drawer | pass |
| Focus returns to the originating row's Detail button | pass |
| Screener scroll region is named, focusable and actually overflows at 1024px, with zero document overflow | pass |
| Calculator hand-off keeps a non-listed venue (`kucoin`) selected | pass after the fix above |
| Status strip's accessible roll-up is announced before the segments | pass — read back as "Platform status: 1 UNKNOWN. Mode: PAPER, no live orders, ever. Market feed: CONNECTED, all 6 books healthy. …" |

These are hand checks, recorded as such. The e2e suite is what turns
them into assertions, and that work is assigned.

### A defect an earlier capture exposed

An earlier `07-settings--*` capture, taken before the Settings sidebar
was corrected, showed the sidebar listing Account / Notifications / Organisation /
Billing **and** the tablist listing Account / Organisation / Billing /
Notifications / Administration — two controls for one choice, in
different orders, which is the duplicate affordance the icon rail was
removed for. Fixed: the tablist owns the in-page categories (Account,
Notifications, Administration) and the sidebar lists only the genuinely
separate routes (`/org`, `/billing`).

That fix exposed a second defect, now also fixed and regression-tested:
with no leaf owning the bare `/settings` path, the route resolved to the
first entry that strips to it — Operations' "Operating mode", whose href
is `/settings#operating-mode` — so a normal user opening Settings saw
the **Operations** destination highlighted and a breadcrumb reading
"Operations / Operating mode".

The Settings captures were retaken: `e4ddad6` regenerated all eleven
`07-settings--*` files, and the post-merge sweep recorded in §0 re-shot
and re-measured them again, so every `07-settings--*` stop in
`overflow-report.json` reflects the corrected page.

**Re-run against HEAD.** An earlier version of this section disclosed
that the report predated the scroll-region naming; an independent audit
pointed out the disclosure still understated it, because the run also
predated the row-identity keys, the Calculator venue fix and then the
whole second round of audit fixes. Rather than narrow the claim again,
the matrix was re-measured:

| | earlier run | current run |
| --- | --- | --- |
| Stops measured | 61 | **77** |
| Stops that failed to prepare, and so were never measured | 2, unnoticed | **0** |
| Viewports | 1440×900, 1024×768, 768×1024, 390×844 | those four **plus 844×390** |
| States covered at 200% zoom | 5 of 7 | **7 of 7** |
| Stops with page-level horizontal overflow | 0 | **0** |
| Scroll regions carrying an accessible name | 0 (all `null` — the artefact contradicted the claim) | **18** |

The 844×390 stop is new and deliberate: a phone in landscape is *above*
the md breakpoint, so the desktop sidebar renders into 390px of height,
and that is the geometry in which `SecondaryNav` collapsed to zero
height and took every secondary destination with it. A capture can only
show that nothing overflows there — the reachability of those entries is
asserted in e2e (`secondary navigation stays reachable on a short
landscape viewport above the md breakpoint`), because that is a
behavioural claim and a screenshot cannot carry it.

The 200% zoom pass now covers all 7 states: it runs the `prepare` steps
too, instead of skipping the two states that need an interaction.

That change immediately found something. The drawer failed to open at
200% zoom and at 390×844, because `RowDrawer` is deliberately
`role="complementary"` at ≥md and `role="dialog"` below it, while the
capture script still waited on the role. Two of those failures existed
in the previous run and went unnoticed, so the matrix was reported as
"75 of 75, zero overflow" when two stops had never been measured.

`capture-after.mjs` now says `N stops MEASURED … M NOT MEASURED
(prepare failed)` and **exits non-zero on a prepare failure**. An
unreachable state is unverified evidence, and the exit code should carry
that rather than the reader's attention.

### An honest limitation of the "after" captures

The "before" screenshots come from the operator's live research session,
with a populated engine. The "after" captures come from a **disposable
in-memory backend**, so several states are legitimately emptier —
`/auto-paper` has no rules, `/paper` no settled cycles, results are
zero. The comparison is therefore sound for **structure, density,
responsiveness, theming and overflow**, and is *not* a like-for-like
comparison of populated data. The audit README makes the same point
about its own captures: the values differ between runs, and neither set
is performance evidence.

## 7. Pages converted to bounded presentation

Every money, bps, price and quantity cell on these routes now goes
through `DecimalValue` with a named preset, keeping the exact string
reachable. Long identifiers (ULIDs) are rendered as identifiers — first
segment plus the full value on hover — rather than competing with the
economics beside them.

| Route | What changed |
| --- | --- |
| `/overview` | per-asset results (realized / marked / net / drawdown / fees), simulated balances |
| `/paper` | realized and marked PnL, per-asset fees, slippage bps, order quantities, prices and fees |
| `/auto-paper` | position net result, rule names joined from `/screener/rules` |
| `/portfolio` | available, reserved, marked equity, intermediate exposure, per-asset results |
| `/pnl` | breakdown net result and average latency |
| `/orders` | requested/filled quantity, average price, fee, latency |
| `/fills` | price, quantity, fee |
| `/screener`, `/calculator` | done |

Tables on these routes also gained `rowKeys` (stable row identity under
a poll) and a `label` on the scroll region (keyboard reachability).

## 8. Risk discoverability

The brief requires risk status and the risk destination to stay
immediately discoverable from Overview **and** Paper Trading.

| Surface | How |
| --- | --- |
| Overview | open breakers appear as an attention item with the backend's own reason text and a link to the risk centre; the pipeline explainer's "rejected at the risk gate" stage links there unconditionally |
| Paper Trading | a risk-status strip directly above the live monitor: open-breaker count, the backend's verbatim reasons, and a link to the risk centre — present whether or not a breaker is open |
| Navigation | `/risk` keeps a standing entry, under Operations › Safety |

A failed risk poll says it could not be read, which is explicitly not
the same as nothing being wrong.

## 9. Outstanding

| Item | State |
| --- | --- |
| Discovery/Calculator implementation | done |
| e2e selector updates for the renamed pause control | assigned — updated to the new names, **not** relaxed |
| Responsive/theme/zoom capture matrix | done — **77 stops measured**, 0 with overflow, **0 unmeasured**, re-measured against HEAD; five viewports, both themes, and 200% zoom on all seven states |
| Keyboard, focus-return, contrast checks in a browser | done — contrast machine-checked (54 assertions); keyboard and focus-return checked by hand on the audited flows; no screen-reader pass |
| Independent diff review | pending |
| Mobile drawer / mobile nav sheet: scrim without `aria-modal` or focus containment | **fixed** — modality now follows the breakpoint (`lib/a11y.ts`): non-modal side panel at ≥md, `role="dialog"` + `aria-modal` + focus containment below it, with focus moved into the sheet on open. The focusable query excludes disabled and invisible controls, which is what a naive trap gets wrong around `PaperControl`'s disabled VIEWER button |
