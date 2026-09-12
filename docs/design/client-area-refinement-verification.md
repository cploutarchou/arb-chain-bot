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

## 1. Repository checks

| Check | Command | Result |
| --- | --- | --- |
| Go formatting | `gofmt -l internal cmd` | clean (no output) |
| Go build | `go build ./...` | pass |
| Go vet | `go vet ./...` | pass |
| Go tests for changed packages | — | **not run: no Go package changed.** `git status` shows zero `.go` files modified; the change is confined to `web/` and `docs/`. The three checks above were still run to prove it rather than assert it. |
| Console lint | `cd web && npm run lint` | pass (ESLint clean + 54/54 contrast assertions) |
| Console typecheck | `cd web && npm run typecheck` | pass |
| Console build | `cd web && npm run build` | *to be re-run — see note below* |
| Console unit suite | `cd web && npm run test:unit` | pass |
| `golangci-lint run ./...` | — | *pending* |
| Playwright e2e | `scripts/e2e.sh` | *pending* |
| GitHub Actions CI | — | **unverified, environment blocked.** `docs/PENDING.md` §0 records repo-wide Actions failure since 2026-08-31 with no runner assigned and no logs, on every branch including `master`. "CI-equivalent" here means the local suite above. |

**On the build row.** `npm run build` passed after the shell and
navigation work, and then a further nine files changed. Lint and
`tsc --noEmit` have been green throughout, but they do not catch what
`next build` catches — notably the framework's own export-shape check on
page files, which this change tripped once already (a helper exported
from `overview/page.tsx`). The row above stays marked pending until the
build is re-run against the final tree, rather than carrying forward a
result that was true earlier.

### What the unit suite does and does not cover

54 tests pass, and it is worth being exact about what that means: they
cover **pure logic** — `lib/decimal.ts`'s presentation rules,
`lib/nav.ts`'s route resolution and access metadata, and the Settings
page's anchor set read out of its source. **Nothing renders a
component.** `ConsoleNav`, `StatusStrip`, `SettingsCategories` and the
rewritten pages have no unit coverage; the navigation *data* is proven,
the navigation *component* is not. The e2e suite is what exercises the
shell in a browser, and the responsive, keyboard and focus checks below
are what exercise the rest. "54 passed" should not be read as component
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
ok: 37 routes mapped, 0 orphans, 39 standing entries
```

| | Before | After |
| --- | --- | --- |
| Primary choices | 6 groups, all expanded by default | 6 destinations + 1 operator area |
| Links on screen at once | 29 group links + 3 pinned = **32** | **18 worst case** (6 primary + 12 secondary, in Operations); 11–13 typical |
| Duplicate group control (icon rail) | present, alongside the label column | removed |
| Pages served | 37 | 37 — unchanged |
| Pages highlighting no nav entry | **3** | **0** |
| Selection keyed on | display label string | stable id + route matching |

Secondary entries per destination: Overview 1, Discover 7, Paper Trading
5, Research & Results 6, Alerts & Rules 4, Settings 4, Operations 12.

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

## 6. Pages converted to bounded presentation

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
| `/screener`, `/calculator` | in progress |

Tables on these routes also gained `rowKeys` (stable row identity under
a poll) and a `label` on the scroll region (keyboard reachability).

## 7. Risk discoverability

The brief requires risk status and the risk destination to stay
immediately discoverable from Overview **and** Paper Trading.

| Surface | How |
| --- | --- |
| Overview | open breakers appear as an attention item with the backend's own reason text and a link to the risk centre; the pipeline explainer's "rejected at the risk gate" stage links there unconditionally |
| Paper Trading | a risk-status strip directly above the live monitor: open-breaker count, the backend's verbatim reasons, and a link to the risk centre — present whether or not a breaker is open |
| Navigation | `/risk` keeps a standing entry, under Operations › Safety |

A failed risk poll says it could not be read, which is explicitly not
the same as nothing being wrong.

## 8. Outstanding

| Item | State |
| --- | --- |
| Discovery/Calculator implementation | in progress |
| e2e selector updates for the renamed pause control | assigned — updated to the new names, **not** relaxed |
| Responsive/theme/zoom capture matrix | pending |
| Keyboard, focus-return, contrast checks in a browser | pending |
| Independent diff review | pending |
| Mobile drawer / mobile nav sheet: scrim without `aria-modal` or focus containment | **found, assigned, not yet fixed** — correctly non-modal on desktop, but full-screen with a scrim on mobile, where modal semantics are right |
