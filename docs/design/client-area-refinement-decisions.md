# Client-area refinement — lead decisions (T-087)

Where the UX spec, the UI spec, the backend contract review and the
implementation disagreed, this is the resolution and the reason. Recorded
so a later session does not relitigate a settled question, and so the
rejected option is visible rather than forgotten.

Inputs: `client-area-refinement-ux.md`, `client-area-refinement-ui.md`,
`client-area-refinement-backend-contract.md`, and the evidence in
`client-area-audit-2026-09-12/`.

---

## D1. The operator area is labelled "Operations", not "Platform administration"

**Both** the UX designer and the implementation independently hit the
same problem, which is worth stating plainly because it is the one place
a navigation-tidiness goal collided with a real access rule.

`internal/auth/rbac.go:46-50` grants VIEWER both `view:risk` and
`view:system`, so `/risk`, `/system` and `/exchanges` are readable by
every current role, and `/audit` sits behind `view:audit`
(OPERATOR/ADMIN) — a **tenant role** permission, not `platform_admin`.

- **Rejected:** gate the whole area on `platform_admin`. It would have
  produced the cleanest label, and it is what a naive reading of "put
  /risk, /exchanges, /system, /audit under an operator administration
  area" suggests. It also would have **removed safety access** that
  VIEWER and OPERATOR have today — exactly what the brief forbids.
- **Rejected:** keep UX's label "Platform administration" with per-entry
  gating. Honest about access, dishonest about the label: a VIEWER would
  see an area named platform administration that contains no platform
  administration they can reach.
- **Decided:** the destination is **Operations**, rendered under an
  explicit **Operator** heading in the primary navigation, and holds
  three groups: Safety (`/risk`), Platform health (`/system`,
  `/exchanges`, `/audit`), and **Platform configuration** — which *is*
  labelled as such and *is* gated on `platform_admin`, entry by entry.

Every route keeps the exact check it had. Asserted in
`web/unit/nav.spec.ts`: no platform-gated entry exists outside this
area, every platform-configuration entry is gated, `/risk`, `/system`
and `/exchanges` stay member-visible, and `/audit` keeps its
`Requires OPERATOR or ADMIN` annotation.

Risk status additionally stays directly linked from Overview and Paper
Trading, so the safety surface is not reached only through Operations.

## D2. The sidebar stays; the top-bar redesign is not taken

The UI spec proposed replacing the 224px sidebar with a 48px top bar as
its primary layout, with a sidebar fallback.

- **Decided:** keep the sidebar, at 240px.
- Reasons: the brief says to build on `ConsoleShell.tsx` rather than
  restructure the shell; the existing dark/light identity and every
  prior design doc are sidebar-based; and by the UI spec's own
  measurement the six labels come to ~970px, clearing 1024 by ~50px —
  one longer destination name tips it into a scroll fallback, and 768px
  never fits at all. A layout whose viability depends on nobody
  renaming "Research & Results" is not the calmer choice.
- The acceptance criterion is met a different way: `PrimaryNav` is
  `shrink-0` and `SecondaryNav` owns the only scroll region, so the
  primary destinations cannot scroll out of view at 1440×900.
- **Consequence accepted:** at 1024px the content column is ~752px, not
  ~976px, so the default Screener column set must be narrower than the
  UI spec's seven-column/848px arrangement. The network-status chips
  leave the default set and move into the detail drawer.

## D3. Rounding, not truncation, at the display boundary

The UI spec specified truncation toward zero, on the grounds that
round-half-up can display a net gain slightly larger than the backend's
value.

- **Decided:** round half-up on the digit string, as implemented in
  `presentDecimal`.
- The UI spec's own analysis names the cost of truncation: it reduces
  magnitude in **both** directions, so a loss also reads smaller than it
  is. For a risk-facing console, systematically understating losses is
  the worse failure; rounding's error is unbiased and bounded by half
  the last displayed digit either way.
- Neither choice is safe without disclosure, which is why
  `DecimalDisplay.rounded` is set whenever a nonzero tail was dropped,
  the exact string is always carried in `exact`, and the spoken form
  states it. The guard is the disclosure, not the rounding mode.
- Both agree on the parts that matter more: no `Number()`/`toFixed()`
  anywhere near a money value, no tiny value collapsing to zero, no
  minus-zero, and no locale-dependent grouping (`toLocaleString()` under
  `de-DE` renders `1.063,11`, which someone copying it back into a size
  field would submit as a different number).

## D4. Tiny values read `-<0.01`, not `>-0.01`

Both forms are true and both keep the sign. `-<0.01` puts the sign in
the conventional leading position, so a column of figures scans
sign-first exactly like every other signed figure beside it; `>-0.01`
requires re-reading the relational operator to recover the sign. The
spoken form spells it out in full ("negative, less than 0.01 USDC,
exactly -0.0000000012"), so the glyphs are never the only carrier.

## D5. Naming: `StatusStrip` for states, summary rows for figures

Accepting the UI spec's collision fix. The UX spec already used "stats
strip" for a row of figures, so the new component for *states* is
`StatusStrip`, and a row of figures stays a plain grid of `Stat`.

## D6. `--shadow-color` is the one accepted token addition

`RowDrawer.tsx:64` hard-codes `-8px 0 24px rgba(0,0,0,.25)` for both
themes, which renders as a black smear against a white panel in light
mode. `--overlay` is a 60% modal scrim and far too heavy; `--border` is
opaque and deliberately decorative. One colour-only token, defined in
all three token blocks (light, `[data-theme="dark"]`, and the
`prefers-color-scheme` block, which `scripts/check-contrast.mjs` asserts
stay identical). No new colour *pair*, so no new contrast ratio to
verify — the 54 existing contrast assertions still pass.

Every other proposed addition (numeric-column tint, tinted status bands,
a neutral-fill token, size/radius tokens) was declined.

## D7. Pause scope is stated, not guessed

The UX spec correctly refused to claim a scope it could not verify. The
backend review then settled it: `POST /api/v1/paper/pause` reaches
`*paper.Engine` only, `internal/screener` has no dependency on
`internal/paper`, and **no pause route exists for Scanner Suite or
rule-based auto-paper at all**.

So the control is renamed from "Pause paper trading" to **"Pause
triangular simulations"**, and states in visible text — not only a
tooltip — that rule-based automatic paper execution keeps running and
has no pause control. The confirmation dialog adds the two further
facts: in-flight cycles still settle, and the engine is shared across
the deployment rather than scoped to one organisation.

This is a **selector change for QA**: three assertions in
`e2e/console.spec.ts` name the old button and dialog text. They are
updated to the new names, not relaxed — the test still proves one-click
reach from an unrelated page, a single confirmation, and a failure toast
carrying the backend's own status and message. The VIEWER assertion
needs no change: the state text "TRIANGULAR PAPER RUNNING" still
matches its `/PAPER (RUNNING|PAUSED)/` pattern.

A genuinely global pause would need a backend change. It has not been
made and is not requested here.

## D8. Zero qualified candidates is explained from real fields

The brief forbids inferring `detected − rejected = qualified`. The
backend review shows why that is not merely cautious but wrong: the
tested invariant is

```
Evaluations = SkippedBooks + NoViableSize + Qualified + Rejected
```

and `skipped_unhealthy`, `no_viable_size` and `dropped_events` are
already in the same `/scanner/status` payload, simply never rendered.

- **Decided:** Overview shows the real four-way stage split, and links
  to the risk page for `reject_reason_counts` — a **live** reason
  histogram that already exists on `/api/v1/risk`.
- **Decided:** state explicitly that the two pre-gate stages have counts
  but **no** reason breakdown. That is the honest limit, and it is
  better said than papered over.
- The T-062 histogram (`bb0d4e8`) is **backtest-only** and explains a
  simulated run's verdict. It is not wired to the live scanner and the
  two are not conflated.

## D9. Auto-paper rule names are joined, never invented

`/screener/auto-paper` returns only `rule_id`. `screener.Rule` from
`GET /api/v1/screener/rules` carries a real `Name`. The console joins on
`rule_id` and shows the real name when there is one; otherwise a
labelled short identifier with the full value available. No name is
fabricated, and no backend change is needed.

## D10. Bugs found by review and fixed in this change

The UI spec's defect table earned its place; three of its findings were
real defects in shared code, fixed here rather than filed:

| Defect | Was | Now |
| --- | --- | --- |
| `signTone("0")` returned `"ok"`, so an exactly-zero result rendered in the positive colour and a flat session read as a winning one | `decimal.ts:98-101` | zero in any spelling returns `"dim"`; `isZeroDecimalStr` added; regression-tested including `-0`, `0e-9` and a tiny nonzero loss |
| `Table`/`VirtualTable` keyed rows by array index, so a 5s poll or a re-sort reused a DOM row for a different record, moving focus and selection silently | `ui.tsx:323`, `ui.tsx:419` | optional `rowKeys` threaded through both; required for any polled or sortable table |
| the `overflow-x-auto` wrapper had no `tabIndex`/`role`/`aria-label`, so Screener's off-screen freshness and action columns were unreachable by keyboard (WCAG 2.1.1) | `ui.tsx` | focusable scroll region with an optional accessible name |

Still open and assigned, not silently dropped: the mobile drawer and
mobile nav sheet render a scrim without `aria-modal` or focus
containment (they are correctly non-modal on desktop, but full-screen
with a scrim on mobile, where modal semantics are right).

## D11. Screener filters move into the URL

The UX spec found that `screener/page.tsx` holds its filters in
component state with no `useSearchParams`/`router.replace`, so the
Calculator hand-off loses them on Back — and that `console-v2.md` §6.1
already required URL sync and never got it. Implementing it satisfies an
existing requirement, is not a URL change (bare `/screener` still
resolves to defaults), and is what makes "return to discovery with
filters intact" possible at all.

## D12. Out of scope, explicitly

- `Ctrl/Cmd+K` route jumper (UX §14) — nothing depends on it.
- Softening backend-authored copy that contains code symbols: the SHADOW
  mode reason is authored at `internal/platform/modes.go:21` and
  asserted in `internal/platform/expansion_test.go:34`. Backend text is
  rendered **verbatim** behind a console-authored plain lead line;
  rewriting it would be a backend change and would break that test.
- Two optional Screener columns the UX spec sketched
  (`Est. net result at default size`, `Venue clock skew`): neither field
  exists on `ScreenerSpreadRow`, and deriving either in the frontend
  would be a fabricated financial aggregate. Not shipped.
- The `platform_admin` / global-`role` coupling the backend review
  surfaced (`authstore.go` sets `platform_admin = (role == ADMIN)`) is
  recorded as a **question for the operator** in the contract review. No
  security finding is asserted and no existing audit conclusion is
  reopened.

## D13. `scanner:config` and `screener:config` are two different permissions

Worth recording because getting it wrong would have introduced a defect
in either direction, and the first read of the code suggested a
frontend/backend mismatch that turned out not to exist.

`internal/auth/rbac.go` defines both, and says so explicitly at
lines 33-39:

| Permission | Gates | Roles |
| --- | --- | --- |
| `scanner:config` (`PermScannerConfig`) | triangular-arbitrage **strategy** config | OPERATOR, ADMIN |
| `screener:config` (`PermScreenerConfig`) | every Scanner Suite **mutation** — settings, rules, templates | **ADMIN only** |

`lib/auth.tsx`'s `can()` lists `scanner:config` in its OPERATOR set,
which matches `PermScannerConfig` exactly, and omits `screener:config`,
so `can(role, "screener:config")` is true only for ADMIN. **The frontend
matrix is correct**; there is no mismatch to fix.

Two consequences for this change:

1. Settings' Administration category gates on
   `can(role, "screener:config") || can(role, "risk:config")` — both
   ADMIN-only. An earlier draft used `scanner:config`, which would have
   shown an OPERATOR forms the backend then refuses.
2. Scanner Suite settings and Strategy & risk are therefore **not**
   marked platform-only, in the navigation metadata or on the page.
   They are deployment-wide settings gated on the ADMIN role, and
   declaring them `platform` would hide them from an ADMIN the backend
   would in fact allow — the mirror image of the bug D1 guards against.
   Asserted in `web/unit/nav.spec.ts`.

So Settings' Administration category has two tiers, and each carries the
check the backend actually performs:

| Tier | Sections | Real gate |
| --- | --- | --- |
| ADMIN role | Scanner Suite, Strategy & risk | `PermScreenerConfig` / `PermRiskConfig` |
| Platform staff | Operating mode, Markets & assets, Venues & fees, AI, Logging & access, Users & roles, Vault, Settings history | `platform_admin` |

A tenant ADMIN without `platform_admin` sees the first tier and, in
place of the second, a sentence saying platform configuration is
operated by platform staff and pointing at their own Organisation page.
Nothing they could previously change becomes unavailable — the backend
already refused all of it with `platform_admin_required`.
