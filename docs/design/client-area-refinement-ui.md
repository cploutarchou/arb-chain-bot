# Client-area refinement — UI specification

Status: DRAFT 2026-09-12. Author: UI design (design-only; no application code
was written or changed in this pass).

**Scope.** The visual layer of the client-area refinement driven by
`docs/design/client-area-audit-2026-09-12/README.md` (desktop dark-mode Brave
session, seven screenshots). This document specifies tokens, layout, nav
chrome, the new status strip, numeric presentation, tables, the row drawer,
the Calculator feasibility hierarchy, component states and accessibility.

**Source of truth.** `web/src/app/globals.css` (tokens) and
`web/src/components/ui.tsx` (primitives). This document *extends*
`docs/design/ui/design-system.md` (cited below as **DS §n**) and is written
against `docs/design/ux/console-v2.md` (**UX §n**). Where this document and
DS/UX disagree, the disagreements are enumerated in §11 and need the lead's
sign-off — they are not silently applied.

**Companion.** `docs/design/client-area-refinement-ux.md` owns the route map,
page composition and flows. Where a decision is behavioural (which segments a
page's strip shows, whether a sort freezes) this document specifies the
*shape* and defers the *content* there, and says so inline.

**Platform non-negotiables inherited unchanged.** Money is a decimal string
rendered from the backend's own value; nothing here recomputes, rounds up, or
re-derives a financial number. Live trading stays disabled until the
production gate. Nothing is described as guaranteed. Exchange keys appear in
no client surface. Every glyph, colour and word is ours. Any number on a
marketing page traces to `docs/campaigns/`.

---

## 0. What this fixes, mapped to the audit

| Audit finding | Fixed by |
|---|---|
| Overview: 12 status cards → 6 counters → more cards; drawdown truncated | §4 `StatusStrip`, §2.4 above-the-fold budget, §5.6 per-asset money stacking |
| Screener: 7 counters + fully expanded filter form eat viewport 1 | §2.4 budget, §6.4 FilterCard common/Advanced |
| Screener: 20+ fractional digits in gross/net bps | §5 bounded precision |
| Screener: table overflows right, freshness/actions off-screen | §6.1 default column set (7), §6.2 bounded scroll region, §3 nav chrome returning 224px of width (conditional — §11.1b) |
| Drawer: horizontal overflow, very long values | §7.2 `min-w-0` + `overflow-wrap`, §7.4 exact-value disclosure |
| Calculator: green net beside "insufficient liquidity"; ellipses | §8 feasibility-first, §8.3 tone suppression on a failed check |
| Paper Trading: repeated state summaries, Danger Zone above history | §2.4 budget (one state surface per page), §2.5 destructive-region placement |
| Auto-Paper: raw rule ID as primary identifier | §6.5 identity column rule |
| Settings: one continuous mixed-purpose form | §2.6 category page shape |

---

## 1. Token inventory

### 1.1 Tokens that exist today (quoted from `web/src/app/globals.css`)

All three blocks (`:root`, `:root[data-theme="dark"]`, and the
`prefers-color-scheme: dark` block guarded by `:not([data-theme="light"])`)
carry the identical full set. Values verbatim:

| Token | Light (`:root`) | Dark | Role this spec uses it for |
|---|---|---|---|
| `--bg` | `#f5f6f8` | `#0b0e14` | page canvas, table body, top-bar-below area |
| `--bg-panel` | `#ffffff` | `#11151f` | status strip, cards, table header, drawer, dialog, filter card, nav bar |
| `--bg-raised` | `#eceef2` | `#171c29` | hover row, active nav, chip "on", exact-value block, disclosure body |
| `--border` | `#d7dbe3` | `#232a3b` | hairline separators only (row rules, strip segment rules, card edges) |
| `--border-strong` | `#7f8797` | `#616a7e` | any boundary that *identifies a control*: inputs, chips, badges, buttons, drawer edge, tab rail |
| `--text` | `#171a21` | `#d7dce7` | primary text, all numerals |
| `--text-dim` | `#4a505e` | `#959db0` | labels, column headers, units, breadcrumb trail, helper copy |
| `--text-gated` | `#6b6e78` | `#888c97` | `GatedControl state="package"` label only |
| `--accent` | `#2f66d0` | `#4f8cff` | links, primary button, focus ring, current-tab rule |
| `--on-accent` | `#ffffff` | `#0b0e14` | ink on an `--accent` fill |
| `--ok` | `#177a45` | `#2fbf71` | `ok` tone |
| `--warn` | `#8a5208` | `#e6a23c` | `warn` tone |
| `--high` | `#a2451a` | `#f0713a` | `high` tone (HIGH severity, liquidity cap hit) |
| `--critical` | `#c8323a` | `#ef5a5e` | `bad` tone |
| `--on-critical` | `#ffffff` | `#0b0e14` | ink on a `--critical` fill (danger hover only) |
| `--pos` | `var(--ok)` | `var(--ok)` | positive signed value |
| `--neg` | `var(--critical)` | `var(--critical)` | negative signed value |
| `--neu` | `var(--text-dim)` | `var(--text-dim)` | zero / n-a / `n = 0` |
| `--heat-n4…n1`, `--heat-0`, `--heat-p1…p4`, `--heat-ink` | see globals.css | see globals.css | funding heatmap only; unused by this refinement |
| `--overlay` | `rgba(0,0,0,0.6)` | `rgba(0,0,0,0.6)` | modal scrim (`ConfirmDialog`, mobile nav sheet, mobile drawer) |
| `--opacity-disabled` | `0.4` | `0.4` | `unbuilt` / `role` gated states, `Button:disabled` |
| `--opacity-stale` | `0.8` | `0.8` | stale row cells, per cell, never the age cell (DS §1.7) |
| `color-scheme` | `light` | `dark` | native form/scrollbar rendering |

`globals.css` also owns the focus ring, which this spec reuses unchanged:

```css
a:focus-visible, button:focus-visible, input:focus-visible,
select:focus-visible, textarea:focus-visible, [tabindex]:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
```

Note the `[tabindex]:focus-visible` selector — it is why §6.2's bounded
horizontal scroll region gets a visible focus ring for free once it carries
`tabIndex={0}`.

`body` already sets `font-feature-settings: "tnum" 1`. §5 keeps it and adds
`font-variant-numeric: tabular-nums slashed-zero` per DS §2 in tables/stats
(already present on `Table`/`VirtualTable`).

### 1.2 Token additions: exactly one

**No new colour is added, and no new colour *pair* is introduced.** Every
foreground/background combination this document specifies already appears in
DS §1.3's contrast table, so this spec adds no ratio that DS §0's contrast
test would not already cover. That constraint is deliberate: it is also why
§4 and §8 use a tone-coloured **left bar plus a tone-coloured word** instead
of a tinted `color-mix` band. A tinted band would create an untabulated
surface, and a status banner must not be the one place in the console whose
contrast nobody has checked.

**Addition 1 of 1 — `--shadow-color`**

| Token | Light | Dark | Kind |
|---|---|---|---|
| `--shadow-color` | `rgba(23, 26, 33, 0.12)` | `rgba(0, 0, 0, 0.45)` | decorative elevation colour |

Justification. `RowDrawer.tsx:64` currently hardcodes the dark shadow in both
themes:

```
shadow-[0_0_0_1px_var(--border),-8px_0_24px_rgba(0,0,0,.25)]
```

DS §4.3 already specifies *two* shadows — `rgba(0,0,0,.25)` dark and
`rgba(23,26,33,.12)` light — but gives the implementer no token to express
the difference, so the light theme inherits a black smear against white.
DS §4.4 then says the notification panel uses "the same shadow as the
drawer", which means the same hardcode would be copied a second time, and
§6.2's scroll-edge cue below wants it a third time.

Why no existing token will do:

- `--overlay` is `rgba(0,0,0,0.6)` — a *modal scrim* for dimming a whole
  viewport. Reusing it as an elevation colour couples two unrelated
  treatments and is 5× too heavy for a light-theme edge shadow.
- `--border` is the decorative hairline and is opaque; it cannot produce a
  falloff, and DS §1.3 pins it at 1.28–1.35:1, i.e. explicitly *not* a
  boundary carrier.
- The drawer's identifying boundary stays the 1px `--border-strong` edge
  (3.36:1 dark / 3.61:1 light per DS §1.3). The shadow is reinforcement
  only, never the sole boundary, so it needs no contrast target of its own.

Shape: the token carries a **colour only**, not a full `box-shadow`, so each
component composes its own offsets and direction:

```
RowDrawer (≥ md, left edge):   0 0 0 1px var(--border), -8px 0 24px var(--shadow-color)
NotificationPanel (downward):  0 0 0 1px var(--border),  0 8px 24px var(--shadow-color)
Sticky table header (§6.2):    0 1px 0 0 var(--border),  0 4px 8px -4px var(--shadow-color)
Scroll-edge cue (§6.2):        inset -12px 0 12px -12px var(--shadow-color)
```

It must be added to **all three** blocks in `globals.css`, like every other
token (DS §1.4 item 4).

### 1.3 Token additions explicitly declined

| Proposed | Declined because |
|---|---|
| numeric-column tint (`--bg-num`) | The audit's table problem is horizontal overflow and unbounded precision, not column discrimination. Right-alignment (`ColumnAlign="num"`, already in `ui.tsx`) plus tabular numerals plus a right-aligned header already separate numeric columns. A tint would add an untabulated surface that every tone would then need re-checking against, for zero legibility gain. |
| tinted status bands (`color-mix(--warn 8%, --bg-panel)`) | Creates a new surface outside DS §1.3. The tone-coloured 3px left bar + tone-coloured state word (§4.2, §8.2) reads at a glance, matches the existing `RestartBanner`/`StaleVersionNotice` convention, and keeps every ratio pre-verified. |
| a "neutral fill" for a failed-check net figure | `--text` on `--bg-panel` is exactly that. §8.3 uses it. |
| a breakpoint/size/radius token set | DS §2.3/§2.4 already fix spacing (4px base; 2/4/8/12/16/24/32/48), radius (4px; 999px for the count pill only) and density. Tailwind utilities express them. |
| a second accent for "current page" | Accessible current-state must not be colour-dependent anyway (§3.5). |

---

## 2. Layout grid and density

### 2.1 Frame

- `main` keeps `min-w-0 flex-1` (it already has it in `ConsoleShell.tsx:866`)
  — this is what stops a wide table from widening the page instead of
  scrolling inside its own region.
- Page gutter: `p-4` (16px) below `md`, `p-6` (24px) at `md` and above.
  Unchanged from today.
- Section rhythm: 24px between sections (`Section` already uses `mb-6`);
  12px between a section heading and its content (`mb-2` + the heading's
  16px line box); 8px between sibling rows inside a card.
- Widths:

| Region | Max width | Reason |
|---|---|---|
| prose / helper copy / empty-state text | `68ch` (≈ 620px) | measure; DS §6 uses the same for marketing |
| a `Section` of summary figures or forms | `1200px` | keeps a 4-up figure grid from stretching to absurd cell widths on a 2560px monitor |
| tables, charts, the status strip | **none** — full available width | the audit's overflow is caused by *lack* of width; capping it would be counterproductive |
| drawer | `min(420px, 50vw)`, floor 360px | §7.1 |

- Column grid for figure/card regions: `grid gap-3` (12px) with
  `grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4`. Four columns
  is the maximum at any width — the audit's 6-across rows at 1440 are what
  produced the truncated drawdown.

### 2.2 Type and density

Unchanged from DS §2.1/§2.3. Restated only where this spec depends on it:
`title` 18/24 600 · `section` 13/16 600 uppercase +0.05em · `body` 14/20 ·
`table` 13/18 · `label` 11/14 uppercase +0.05em · `badge` 11/14 500 ·
`button` 12/16 500 · `mono` 12/16. Table row height stays **31px**
(`ROW_HEIGHT` in `ui.tsx:346`) and rows stay single-line — §6.1's column
decisions are constrained by that and say so.

### 2.3 Control sizes

| Control | Height | Min hit area | Notes |
|---|---|---|---|
| `Button` default | 26px (`px-2.5 py-1`, 12/16) | 26×26 | today's size, unchanged |
| `Button` in a table cell | 26px | 26×26 | Screener's `Detail` |
| Shell persistent control (pause/resume, menu, theme, bell) at `< md` | **44px** | 44×44 | touch; see §3.6 |
| Shell persistent control at `≥ md` | 32px | 32×32 | today's bell/theme sizes |
| filter chip | 24px | 24×24 | DS §4.7 |
| numeric filter input | 26px | — | `NumericFilterField`, unchanged |
| strip segment (when it links) | 44px (whole segment) | full segment | §4.2 |
| drawer close | 32×32 | 32×32 | unchanged |

WCAG 2.5.8 (AA) target minimum is 24×24 CSS px; every size above clears it,
and the shell's persistent controls clear 44×44 at touch widths.

### 2.4 The above-the-fold budget (the core density rule)

This is the rule that fixes Overview and Screener. It is checkable, so QA
can fail a build on it.

> **Between the page title and the first primary data region (the table,
> chart, history list or result the page exists to show), a page may render:
> at most one `StatusStrip` (one row, ≤ 7 segments), at most **four**
> primary summary figures, and at most one filter card in its default
> collapsed-Advanced state. The whole region must measure ≤ 200px tall at
> ≥ 1024px viewport width, and ≤ 33% of viewport height at any width.**

Consequences:

- **Four** is the cap on primary figures, not six and not twelve. A figure
  qualifies as primary only if a user acts differently depending on its
  value. Overview's twelve status cards are *states*, not figures — they
  become strip segments (§4). Screener's seven counters reduce to: the
  strip (venues online, feed age, poll interval, exclusions roll-up) plus
  at most two figures.
- Everything demoted goes into a **labelled secondary section** below the
  primary data region, using the existing `Section` heading style, with the
  section name saying what it is for: `Diagnostics`, `Exchange health`,
  `Session counters`, `Engine internals`. Secondary sections are collapsed
  by default via the §6.4 disclosure control (`aria-expanded`,
  `aria-controls`, chevron), with the count in the toggle
  (`Diagnostics (9)`), and their state persists per user in `localStorage`
  under `arb.section.<page>.<slug>` with the same try/catch discipline as
  `ConsoleShell`'s existing `GROUP_STORAGE_KEY` handling.
- A page never repeats a state it already shows. Paper Trading currently
  shows engine state in the strip cell, again in `LIVE CYCLES (IN FLIGHT) →
  ENGINE`, and a third time in the shell's `PAPER RUNNING` label
  (`05-paper.png`). The shell indicator (§3.6) is authoritative; the page
  shows engine state **once**, in its strip, and `PaperControl`'s
  `hideStateLabel` prop (already in `PaperControl.tsx:41`) is the mechanism.

### 2.5 Destructive regions

A destructive region (`Danger Zone`, reset, revoke) is always the **last**
section of a page, below every read surface, and never above history. Visual
treatment unchanged from today: 1px `--critical` border on `--bg-panel`,
`p-3`, a 13/18 600 `--critical` heading, consequence sentence in `--text`,
`Button danger` for the action, the precondition sentence beside the
disabled button in `--text-dim`. No fill, no icon badge — DS §1.5 permits a
`--critical` fill only on `Button danger` hover.

### 2.6 Settings category shape

Settings stops being one scroll. It becomes a category index plus a
category body, preserving every existing deep link (`#markets`, `#users`,
`#platform-versions`, …):

- At `≥ lg`: a 200px left category list inside the page (not the shell),
  `--bg-panel`, 1px `--border`, 32px rows, current category `--bg-raised` +
  500 weight + 2px `--accent` left bar + `aria-current="page"`; body to its
  right, `max-w-[1200px]`.
- At `< lg`: the category list becomes a horizontal tab rail identical to
  §3.4's secondary nav, scrollable horizontally inside its own region.
- Categories (grouping is UX-owned; this is the shape, and the audit's
  account/organisation/billing/notifications vs platform-administration
  split is the constraint): each category is a page-level region with its
  own `PageTitle`-sized 18/24 600 heading, its own version chip and
  `Updated Ns ago`, and its own apply/preview/confirm cycle. Two categories
  never share a form.
- Every deep link that exists today resolves to `?cat=<slug>#<anchor>` and
  scrolls to the anchor with the category already selected. A hash that no
  longer maps renders the category index with `Empty what="section for
  that link"` rather than a blank body.
- Helper copy loses implementation names. `Engine.Run`, `backend wiring`
  and task identifiers do not appear in a label, a helper line or a radio
  description; the option that is not available says *what* is unavailable
  in product terms and carries the `unbuilt` treatment (DS §1.6). The
  `SHADOW` row in `07-settings.png` is the specimen to rewrite; the copy
  itself is UX's.

### 2.7 Behaviour at the five required viewports

Tailwind breakpoints in use: `sm` 640 · `md` 768 · `lg` 1024 · `xl` 1280.

| Viewport | Nav chrome (§3) | Content | Strip (§4) | Table (§6) | Drawer (§7) |
|---|---|---|---|---|---|
| **1440×900** | top bar 48px + secondary tab rail 36px, full labels | gutter 24px, content width 1392px | one row, ≤7 segments, 44px tall | 7 default columns fit without scrolling | right panel 420px |
| **1024×768** | same; if the six labels exceed the bar, the bar scrolls horizontally inside its own region (§3.3) | gutter 24px, content **976px with the §3.2 top bar**, 752px under the §11.1b sidebar fallback | one row, ≤7 segments | 7 default columns (848px) fit **only with the top bar**; under the fallback the region scrolls — see §6.1 | right panel `min(420, 50vw)` = 420px |
| **768×1024** | same top bar; secondary rail scrolls | gutter 24px, content 720px | **two rows of 3–4** (`grid-cols-3`) | region scrolls horizontally; Pair column sticky-left; the `Detail` button column is pinned right so the action is never off-screen | `min(420, 50vw)` = **384px** |
| **390×844** | 48px bar: brand + mode chip + pause control + hamburger; nav in a full-height sheet | gutter 16px, content 358px | **single column** definition-list, 32px rows, label left / state right | region scrolls; sticky Pair column; card-shaped row alternative is **not** introduced (UX §8: the drawer is the mobile read path) | **full-screen, modal** (scrim + focus trap, §7.5) |
| **200% zoom** | 1440 at 200% = 720 CSS px ⇒ **below `md`, `sm` active** ⇒ the mobile chrome (bar + sheet) at 720px. 1024 at 200% = 512 CSS px ⇒ below `sm` ⇒ the 390 band. | reflow only; no separate zoom branch exists | 720px ⇒ `sm` band: **2-up grid** (§4.4). 512px ⇒ single column. | as 390: scrolling region, sticky Pair column | full-screen modal |

The 200%-zoom row is the reason §3.6's mobile paper-mode indicator matters
beyond phones: **the zoom path and the mobile path are the same path.** If
the pause control only exists inside the hamburger sheet, a 200%-zoom user
loses it, which is a WCAG 1.4.4 reflow failure dressed as a mobile
nicety. It stays in the bar itself.

**No page-wide horizontal overflow, at any of the five.** Enforcement rules:

1. No element sets a `min-width`, fixed `width`, or `white-space: nowrap`
   run that can exceed the viewport, except inside a §6.2 scroll region.
2. The only elements permitted to scroll horizontally are: the §6.2 table
   region, the §3.3 nav bar overflow, and the §3.4/§2.6 tab rail. Each is a
   labelled, focusable region.
3. Long unbroken strings (rule IDs, ULIDs, `docs/…` paths, exact decimals)
   use `overflow-wrap: anywhere` and wrap; they never widen a container and
   never get an ellipsis that hides the tail (§5.5).
4. The drawer is `position: fixed` and therefore cannot widen the page; its
   *internal* overflow is a separate bug, fixed in §7.2.

---

## 3. Navigation chrome

### 3.1 Recommendation: remove the icon rail

**Remove it.** Reasons, in order of weight:

1. **It is redundant with the control beside it.** `IconRail`'s `onSelect`
   calls `expand(title)` + `scrollIntoView` (`ConsoleShell.tsx:739-745`) —
   exactly what clicking the adjacent `NavGroupHeader` does, except the
   header also collapses and carries `aria-expanded`/`aria-controls`. Two
   controls, one behaviour, one of them unlabelled to sighted users.
2. **Its subject disappears.** The rail is one glyph *per group*. The
   refinement replaces 6 groups × 29 links with 6 primary destinations and
   contextual secondary nav. With no group layer, the rail has nothing to
   address: it would degrade to a second, iconographic copy of the primary
   nav.
3. **It costs the width the audit needs.** Rail 44px + label column 176px =
   220px (DS §4.1), realised as `w-56` = 224px in `ConsoleShell.tsx:847`.
   At 1024 that is 22% of the viewport spent on navigation while the
   Screener table runs off the right edge.
4. **It is desktop-only anyway.** UX §8 already excludes it below `md`, so
   it is not load-bearing for the mobile or 200%-zoom path.

Counter-argument considered and rejected: a collapsed-sidebar icon-only mode
(DS §4.1) is genuinely useful on constrained screens. But with six
destinations the whole primary nav fits in a 48px **top bar** at every
width, which returns *more* space than a collapsed rail and needs no
collapse state, no tooltip-on-focus machinery, and no per-user persistence.

This contradicts UX §2.1 and DS §4.1, which both specify the rail. See
§11.1a — lead decision required, because DS is a published spec. The
separate, larger question of whether the sidebar becomes a top bar at all is
§11.1b, which carries a fallback that keeps a 208px sidebar.

### 3.2 Frame

```
┌──────────────────────────────────────────────────────────────────────┐
│ ARB CONSOLE   Overview  Discover  Paper  Research  Alerts  Settings  │ 48px  --bg-panel
│                              [● PAPER] [Pause]  [🔔 2] [☀]          │
├──────────────────────────────────────────────────────────────────────┤
│ Screener   Perpetuals   Funding   Calculator   Evidence              │ 36px  --bg-panel
│ ─────────                                                            │       2px --accent under current
├──────────────────────────────────────────────────────────────────────┤
│ Discover / Screener                                                  │ 20px  breadcrumb, --text-dim
│ Screener                                                             │       PageTitle 18/24 600
```

- **Primary bar**: 48px, `--bg-panel`, bottom rule 1px `--border`,
  `position: sticky; top: 0; z-index: 30`. Contents left→right: brand
  (14/20 600, +0.02em, `--text`, links to Overview), the six destination
  links, then a right cluster: mode chip, paper pause/resume, notification
  bell, theme toggle.
- **Secondary bar**: 36px, `--bg-panel`, bottom rule 1px `--border`, sticky
  below the primary bar (`top: 48px`). Renders only when the current
  destination has children. `aria-label="{Destination} sections"`.
- **Breadcrumb**: 20px row inside `main`'s padding, `label`-adjacent size
  12/16, `--text-dim`, separator a literal `/` in a `aria-hidden` span with
  4px margins. Rendered only at depth ≥ 2. The last crumb is the current
  page, is **not** a link, and carries `aria-current="page"`.
- The mode banner's full sentence (`PAPER TRADING ONLY — live execution
  permanently disabled`) no longer fits a bar. It moves to a **48px
  full-width band directly under the secondary bar** at every width, keeping
  DS/UX's "chrome, not nav" placement and its existing 1px tone border +
  8px dot + text treatment from `ConsoleShell.tsx:365-384`. The compact chip
  in the bar is the always-visible short form (§3.6).

### 3.3 Primary destination item

Labels below are the audit's recommended six; the final label set and route
mapping are UX's (§11.2).

| Slot | Label |
|---|---|
| 1 | Overview |
| 2 | Discover |
| 3 | Paper Trading |
| 4 | Research & Results |
| 5 | Alerts & Rules |
| 6 | Settings |

Geometry: 36px tall, `px-3`, 13/18, radius 4, 4px gap between items,
optional 16px leading glyph (`NavIcon`, stroke 1.3 — DS §3).

| State | Text | Background | Extra |
|---|---|---|---|
| default | `--text-dim` | none | — |
| hover | `--text` | `--bg-raised` | — |
| **current** | `--text`, **600 weight** | `--bg-raised` | **2px `--accent` bottom rule, inset 0 left/right**, plus `aria-current="page"` |
| current, in a descendant page | `--text`, 600 | `--bg-raised` | same rule; the secondary bar carries the leaf |
| focus-visible | inherits | inherits | globals.css ring |
| `unbuilt` / `role` | `GatedControl as="nav" state=…` | — | DS §1.6, unchanged |
| `package` | `GatedControl as="nav" state="package"` | — | `--text-gated` + 12px lock + `Badge dim`; never in the operator console |

Overflow: if the six labels plus the right cluster exceed the bar width, the
destination list becomes a horizontally scrollable region —
`role="group" aria-label="Primary navigation" tabIndex={0}` with
`overflow-x:auto` and the §6.2 scroll-edge cue — and the right cluster keeps
its place (`shrink-0`). The nav never wraps to two rows and never truncates
a label to an ellipsis.

### 3.4 Secondary (contextual) navigation

A horizontal tab rail, not a dropdown: the whole sibling set stays visible,
which is the point the audit makes about Auto-Paper's relationship to Paper
Trading being hard to see.

- Tab: 36px tall, `px-3`, 13/18, `--text-dim`; hover `--text`; **current**
  `--text` + 600 + **2px `--accent` bottom rule** + `aria-current="page"`.
- Rail: `role="navigation" aria-label="{Destination} sections"`, a `<ul>` of
  links (real `<a href>`, not `role="tab"` — these navigate).
- Overflow: horizontally scrollable region, same treatment as §3.3.
- At `< md` the rail stays a rail (it scrolls); it does **not** collapse
  into the hamburger sheet, because the sheet is for switching
  destinations, and losing the sibling context is the audit's Auto-Paper
  complaint.
- The rail is where the 29 links land. Example shapes (UX owns the final
  mapping): Discover → Screener · Perpetuals · Funding · Calculator ·
  Evidence; Paper Trading → Session · Simulation history · Auto-Paper;
  Research & Results → Reports · Campaigns · Replay & Backtesting ·
  PnL & Analytics.

### 3.5 Accessible current-page state (never colour alone)

Four simultaneous carriers, so no single channel is load-bearing:

1. `aria-current="page"` on the anchor — the programmatic carrier.
2. **600 weight** on the label — shape, not colour.
3. `--bg-raised` background — luminance, not hue.
4. 2px `--accent` bottom rule — position, not hue.

At `forced-colors: active` the accent rule falls back to `Highlight` and the
weight and `aria-current` still carry; nothing depends on the rule alone.
The breadcrumb's last crumb repeats the current page as text, which is a
fifth, purely textual carrier at depth ≥ 2.

### 3.6 Persistent paper-mode indicator and pause/resume at every width

Non-negotiable: both are in the **bar itself** at every width, never only
inside the hamburger sheet (see §2.7's 200%-zoom note).

| Width | Mode indicator | Pause/resume |
|---|---|---|
| `≥ lg` | compact chip: 8px dot (`aria-hidden`) + mode word, 11/14 600, 1px border in the tone, `px-1.5 py-0.5` — today's `ModeBanner compact` markup (`ConsoleShell.tsx:345-363`), unchanged | full `Button danger` / `Button`, label `Pause paper trading` / `Resume paper trading`, 26px |
| `md`–`lg` | same chip | same button, label shortened to `Pause` / `Resume`, with `aria-label` keeping the full sentence |
| `< md` | same chip, in the bar's second slot; when the bar is tight the chip keeps the dot + word and drops nothing (the word is the state carrier) | **44×44 icon button** with `aria-label="Pause paper trading"` / `"Resume paper trading"`, glyph = a 20px pause/play mark, plus a visually-hidden text label; the confirm dialog is unchanged |
| any | the full-sentence band under the secondary bar (§3.2) | — |

The restart-pending annotation (`configured: X — restart pending`) stays on
the full-width band, in `--warn`, never only as the `*` the compact chip
shows today — the `*` keeps its `aria-label="restart pending"` as a
secondary indicator.

Mode is *text* in every form. There is no state expressed only by the dot's
colour.

### 3.7 Mobile navigation sheet (`< md`)

Unchanged mechanics from today (`ConsoleShell.tsx:820-845`), restated with
the new content: hamburger 44×44, `aria-expanded`, `aria-controls`; sheet
`w-72 max-w-[85vw]`, `--bg-panel`, `--overlay` scrim, Escape closes and
returns focus to the hamburger (already implemented at
`ConsoleShell.tsx:765-772`), tap-outside closes. Contents: the six
destinations as 44px rows, each with its section list nested beneath as
36px rows, current page per §3.5. The mode band and paper control are
**not** repeated in the sheet — they are in the bar.

Gap to close: the sheet has no focus containment and no `aria-modal`. It has
a scrim, so it *is* modal, and it must contain focus like `ConfirmDialog`
does (`ui.tsx:552-575`). Same fix as §7.5.

### 3.8 What happens to the 29 links

Nothing is deleted. Every route stays reachable: primary destination →
secondary rail → (for admin/system surfaces) a Settings category (§2.6) or
a `Diagnostics` section. Operator-only surfaces stay operator-only; the
client console's shorter set is UX §1/§2.2's existing distinction and is not
re-litigated here.

---

## 4. `StatusStrip` — new component

Replaces Overview's wall of twelve status cards (`01-overview.png`) and is
available to any page. It shows **states**, never money and never counters —
those are §5 figures or table cells.

Naming: UX §2.3 already uses "stats strip" for a row of `Stat` *figures*.
These are different components with confusingly similar names. This document
calls the new one **`StatusStrip`** and asks that UX §2.3's row of figures be
renamed **`SummaryRow`** (§11.3).

### 4.1 Contract

```ts
type StripState = "ok" | "warn" | "critical" | "unknown" | "loading";

interface StripSegment {
  /** Short uppercase label, ≤ 14 chars: "FEED", "VENUE CLOCK", "DATABASE". */
  label: string;
  /** The backend's own state word, rendered verbatim and uppercased for
   *  display only: "DEGRADED", "CONNECTED", "IDLE", "READY". Required —
   *  there is no icon-only or colour-only segment. */
  word: string;
  state: StripState;
  /** Optional one-line qualifier, ≤ 24 chars: "3 of 15 venues", "514ms". */
  detail?: string;
  /** Optional destination; makes the whole segment a link. */
  href?: string;
  /** For state="unknown": the verbatim backend reason, shown as `detail`
   *  when short enough, otherwise in the segment's drawer/diagnostics row.
   *  Never only in a `title`. */
  reason?: string;
}

function StatusStrip(props: {
  /** 1–6 segments. A 7th is a spec violation, not a wrap. */
  segments: StripSegment[];
  /** Accessible name: "System status", "Screener status". */
  label: string;
  /** Renders the leading roll-up segment (default true). */
  rollup?: boolean;
}): JSX.Element;
```

### 4.2 Visual

Container: `--bg-panel`, 1px `--border`, radius 4, `role="group"`
`aria-label={label}`. One row at `≥ lg`, height **44px**. Segments are
`flex-1 min-w-0`, separated by a 1px `--border` vertical rule (`divide-x`),
each `px-3 py-1.5`.

Segment internals, two lines:

```
FEED                       ← label: 11/14, 500, uppercase, +0.05em, --text-dim
● DEGRADED  3 of 15        ← dot 8px (aria-hidden) + word 13/18 600 in the
                             state token + detail 12/16 --text-dim
```

Roll-up segment (leading, `shrink-0`, min-width 132px): label `STATUS`,
word = the worst state's count in words — `ALL OK`, `1 DEGRADED`,
`2 CRITICAL`, `1 UNKNOWN` — in that state's token. This is a roll-up of
segment *states*, not of any number, so it recomputes nothing financial.
Segments keep a **fixed declared order** and never re-sort on a poll; the
roll-up is what surfaces attention.

| State | Word colour | Dot | Word examples | Notes |
|---|---|---|---|---|
| `ok` | `--ok` | `--ok` | `OK`, `RUNNING`, `CONNECTED`, `READY` | no fill anywhere (DS §1.5) |
| `warn` | `--warn` | `--warn` | `DEGRADED`, `IDLE`, `RESTART PENDING` | |
| `critical` | `--critical` | `--critical` | `DOWN`, `DISCONNECTED`, `STALE` | |
| `unknown` | `--text-dim` | `--text-dim`, hollow (1px ring, transparent centre) | `UNKNOWN` + `reason` as `detail` | the hollow dot is a *shape* difference from `ok`, so the unknown/ok distinction survives greyscale |
| `loading` | `--text-dim` | none; a 8px `--border` square placeholder | literal `…` | `aria-busy="true"` on the segment; **never** a fabricated value, never a shimmer |

Interaction, when `href` is set: the whole segment is the link. Hover
`--bg-raised` (state tones on raised are all ≥ 4.55:1 per DS §1.3, both
themes). Focus-visible → globals.css ring, inset by the 2px offset so it is
not clipped by the container. Non-linking segments are inert (`<div>`), not
disabled buttons.

### 4.3 How text carries state

Every segment's state is readable with all colour removed: the **word** is
the state (`DEGRADED`, not a red dot), and the dot differs in *shape* for
`unknown` (hollow) and is *absent* for `loading`. The roll-up repeats the
worst state as a word plus a count. The container's accessible name
composes to, e.g., `System status: 1 degraded, 5 ok` via an `sr-only` first
child that mirrors the roll-up, so a screen-reader user gets the summary
before the six segments.

### 4.4 Narrow degradation

| Width | Layout |
|---|---|
| `≥ lg` (1024+) | one row, roll-up + up to 6 segments, 44px, vertical `--border` rules |
| `md`–`lg` (768–1023) | `grid grid-cols-3 gap-0` inside the same card, 44px rows, 1px `--border` rules between cells both ways; roll-up spans the full first row |
| `sm`–`md` (640–767) | `grid-cols-2` |
| `< sm` (incl. 390 and every 200%-zoom case) | single column definition-list: 32px rows, label left (`--text-dim`, 11/14 uppercase), dot + word + detail right-aligned; roll-up is the first row with a 1px `--border` bottom rule and 600 weight |

Never horizontally scrollable, never truncated to an ellipsis: labels are
capped at 14 characters by contract, and `detail` drops before `word` if a
row cannot fit (`word` is the state carrier and is never dropped).

### 4.5 Recommended Overview mapping

Content is UX-owned; this is the shape the audit's twelve cards reduce to.

Strip (6): `MODE` · `FEED` · `VENUE CLOCK` · `PAPER ENGINE` · `RECORDER` ·
`DATABASE`. Primary figures (≤4, §2.4): `Unresolved alerts` ·
`Realized PnL (session)` per asset · `Drawdown` per asset ·
`Breakers open`. Everything else — `Scanner`, `Fees paid`, the six session
counters, `Revalidations before execution`, the four exchange-health
counters — moves into `Session counters` and `Exchange health` secondary
sections, collapsed by default with counts in their toggles.

### 4.6 Page adoption

| Page | Strip segments (recommended) |
|---|---|
| Overview | §4.5 |
| Screener / Perpetuals / Funding | `VENUES` (`14 / 15`, warn when < total) · `FEED AGE` · `POLL` · `EXCLUDED` (roll-up of suspect + unknown-liquidity counts as a `detail`, linking to the §6.4 Advanced toggles) |
| Paper Trading | `ENGINE` · `IN FLIGHT` · `STORAGE` |
| Auto-Paper | `RULE` (enabled/disabled) · `EVIDENCE` (`THIN`, `n = 0`) · `OPEN POSITIONS` |
| Calculator | none — the feasibility block (§8.2) is the status surface |

---

## 5. Numeric presentation

Hard constraint: **no displayed number is ever recomputed.** Everything
below is string and digit manipulation on the backend's own decimal string,
in the spirit of and alongside `web/src/lib/decimal.ts`'s existing
helpers. Nothing here divides, multiplies, or parses to a JS `number`.

### 5.1 Display helper contract

Add to `web/src/lib/format.ts` (display-only file, per its own header):

```ts
export interface NumDisplay {
  /** Bounded, grouped, sign-free text for the cell. */
  text: string;
  /** The backend's original string, untouched — the disclosure value. */
  exact: string;
  /** True when digits were dropped, i.e. `text` is not the whole value. */
  bounded: boolean;
}

/** Fixed decimal places: money, bps, percent, PnL. Pads to exactly `dp`. */
export function fmtDp(raw: string, dp: number): NumDisplay;

/** Significant digits: prices and base quantities, whose magnitude varies
 *  by orders of magnitude across rows. Trims trailing zeros. */
export function fmtSig(raw: string, sig: number): NumDisplay;

/** Integers: counters. Grouped, 0 dp. */
export function fmtCount(raw: string | number): NumDisplay;
```

Rules, in order:

1. **Validate, never invent.** Accepted shape is `^[+-]?\d+(\.\d+)?$`.
   Anything else (`""`, `null`, `"unknown"`, `"—"`, a scientific-notation
   string, a malformed payload) returns `{ text: raw ?? "—", exact: raw ??
   "—", bounded: false }`. A malformed value renders as itself. It is never
   coerced to `0`, never blanked, never `NaN`.
2. **A leading `+` is accepted** and stripped from `text`. This matters:
   `signedText()` (decimal.ts:103) *prepends* `+`, so the composition order
   is fixed below and the formatter must tolerate either order without
   rejecting the string.
3. **Truncate toward zero.** Excess fractional digits are dropped, not
   rounded. Stated plainly, including the asymmetry: truncation toward zero
   reduces magnitude in *both* directions, so a gain reads no larger than
   it is **and a loss reads no larger than it is**. That is a real
   trade-off and the reason §5.5's disclosure is mandatory rather than
   optional — a user comparing two near-identical rows must be able to
   reach the full value. The alternative (round-half-up) was rejected
   because it can display a net gain larger than the backend's value, and
   a money console must never round a result upward for presentation.
4. **Grouping.** Comma every three integer digits; `.` as the decimal mark.
   **Fixed, not locale-derived.** `toLocaleString()` is forbidden here:
   under `de-DE` it renders `1.063,11`, which a user copying the value back
   into a `size_quote` field would submit as a different number, and the
   backend parses `.`-decimals. Grouping is applied to the integer part
   only.
5. **Padding.** `fmtDp` pads the fraction to exactly `dp` so a column's
   decimal points align under `tabular-nums`. `fmtSig` and `fmtCount` do
   not pad.
6. **Tiny nonzero values never read as zero.** If bounding would produce an
   all-zero `text` while `exact` has a nonzero digit, `text` becomes a
   less-than form at that precision: `<0.01` for positives, `>-0.01` for
   negatives (dp = 2 shown; generally `<` / `>-` followed by `1` in the
   last retained place). `bounded` is `true`. A `sr-only` expansion
   accompanies it: `less than 0.01` / `greater than minus 0.01`, so the
   `<`/`>` glyphs are never the only carrier.
7. **No minus zero, ever.** If every digit of `exact` is `0`, `text` is the
   padded zero (`0.00`) with **no sign**, regardless of a leading `-` in
   the input, and the tone is `--neu`. `-0.00`, `-0`, and `+0.00` must not
   appear. (`signedText()` already suppresses `+` on zero; the `-0` case is
   handled here.)

### 5.2 Precision table

| Quantity | Helper | Unit label | Example in / out |
|---|---|---|---|
| spread bps (gross, net) | `fmtDp(v, 2)` | `bps` in the column header | `+1063.1123595505617977` → `+1,063.11` |
| percent, APR | `fmtDp(v, 2)` | `%` | `0.0712` → `0.07` |
| funding rate percent | `fmtDp(v, 4)` | `%` | `0.0100123` → `0.0100` |
| quote money (PnL, net, fees, liquidity) | `fmtDp(v, 2)` | asset code per row (§5.6) | `106.31123595505617` → `106.31 USDT` |
| price (ask, bid) | `fmtSig(v, 6)` | none (the pair carries it) | `0.01972` → `0.01972`; `2524.5` → `2524.5` |
| base quantity | `fmtSig(v, 6)` | base asset code | `56179.775280898876` → `56,179.7` |
| counters | `fmtCount(v)` | none | `435990` → `435,990` |
| hit rate | `fmtDp(v, 1)` + `%` | `%`, always beside `n =` | `0.0` → `0.0 % (n = 0)` |
| age / latency | existing `bookAgeText` / `fmtAge` | — | `STALE 11.2s` canon per DS §1.7 |
| duration | existing `fmtDurationSec` | — | `1h 2m 3s` |
| bytes | existing `fmtBytes` | — | unchanged |

Prices use significant digits rather than fixed decimals precisely so
`0.0178` and `2524.5` can share a column without either being destroyed;
`fmtSig` also means a price never collapses into the §5.1 rule 6
less-than form.

### 5.3 Composition order with `signedText` / `signTone`

Fixed, and it must be documented at the call site:

```
const d = fmtDp(row.spread_bps_net, 2);   // 1. bound the raw string
const text = signedText(d.text);          // 2. then apply the sign
const tone = signTone(row.spread_bps_net); // 3. tone from the RAW value
```

Tone reads the **raw** string, never the bounded text — otherwise a value
that bounds to the §5.1 rule 6 form would lose its sign classification.

Two live defects this exposes, both in the "where code and spec disagree,
the code is wrong" sense of DS's preamble:

- `signTone()` (`web/src/lib/decimal.ts:98-101`) returns `"ok"` for `"0"`,
  so a true zero renders **green**. DS §1.8 requires zero → `--neu`. It
  needs a third branch: all-zero digits → `"dim"`.
- `web/src/app/screener/page.tsx:470` does
  `netTone === "ok" ? "text-[var(--pos)]" : "text-[var(--neg)]"` — the same
  binary, so zero is green there too. It must map three ways:
  `ok → --pos`, `bad → --neg`, `dim → --neu`.

### 5.4 Alignment, numerals, units

- Numeric columns and their headers are right-aligned via the existing
  `ColumnAlign = "num"` (`ui.tsx:274`). Passing `align` is mandatory for
  any new numeric column.
- `font-variant-numeric: tabular-nums slashed-zero` on every table and stat
  (already on `Table`/`VirtualTable`; add to `Stat`'s value and to the
  drawer's `<dd>`). `body` keeps `"tnum" 1`.
- The sign is part of the string, so right-aligned columns have a ragged
  sign edge. That is correct and intended (DS §2.2): tabular numerals keep
  the *digits* aligned.
- Units: in the **column header** when constant for the column
  (`Net (bps)`, `Liquidity (quote)`); in the **cell**, after the number,
  when it varies per row (`106.31 USDT`). A bare figure that needs its
  column header to be understood is acceptable in a table; a bare figure in
  a `Stat`, a strip `detail`, a drawer row or a Calculator result is not —
  those always carry the unit or asset code inline.
- Precision note in the header: a bounded numeric column's header carries a
  second line, 11/14 `--text-dim`: `2 dp · exact in Detail`. This is what
  makes the whole column legible as *display-rounded* without a per-cell
  marker, and it points at the disclosure.

### 5.5 Exact-value disclosure (accessible, not `title`-only)

Three tiers, and `title` is never the only one:

1. **Convenience**: every bounded cell/figure gets `title={exact}`. Mouse
   users get it free. It is explicitly *not* an accessibility mechanism —
   `title` has no guaranteed AT exposure and no keyboard path (UX §9 makes
   the same point about rail labels).
2. **In a table**: the column header's `2 dp · exact in Detail` note plus
   the row's `Detail` action, which opens the drawer. The drawer carries an
   **`Exact values`** disclosure (§7.4): a `<details>`-style toggle
   (`aria-expanded`, `aria-controls`, chevron 16px) whose body is a
   `--bg-raised` block, `mono` 12/16, `overflow-wrap: anywhere`, one
   `label: value` line per field, values verbatim. Keyboard-reachable,
   selectable, copyable.
3. **Outside a table** (`Stat`, strip `detail`, Calculator result): ≤ 6
   figures per region, so each bounded figure also carries an `sr-only`
   suffix — `, exact value 1063.1123595505617977`. This is not done
   per-cell inside a 500-row table, where it would make every row
   announcement unusable; the drawer is the table's disclosure.

`Stat` today already sets `title={String(value)}` (`ui.tsx:43,59`) — that
stays, and the `sr-only` suffix is added beside it. `Stat`'s
`mt-1 truncate` (`ui.tsx:59`) is the ellipsis the audit saw on the
Calculator; §5.6 replaces truncation with stacking.

### 5.6 Per-asset money: USDC and USDT are never summed

- Money is always rendered **per asset**, each with its own asset code.
  There is no total row, no combined figure, and no cross-asset arithmetic
  anywhere in the console. This is a display rule with a hard reason: USDC
  and USDT are different assets, and adding them would fabricate a number
  the backend never produced.
- Inline form (≥ 2 assets, ≥ `md`, fits): `0.00 USDC · 0.00 USDT` — a
  `·` (U+00B7) with 8px margins, matching what Overview already renders.
- **Stacked form** replaces truncation whenever the inline form does not
  fit, and always below `md`: a `<dl>` of one row per asset, 20px rows,
  value `--text` right-aligned + asset code `--text-dim` 11/14 after it.
  `Stat` drops `truncate` for these and grows vertically. **This is the fix
  for the truncated drawdown figure** in `01-overview.png`: the card grows,
  the number does not get cut.
- More than 3 assets: show 3 stacked rows plus `+N more` as a text button
  opening the drawer or the relevant balances table. Never an ellipsis.
- `min-w-0` on the flex/grid child holding a per-asset group, or the
  stacking will not take effect.

---

## 6. Table spec

Built on the existing `Table` / `VirtualTable` (`ui.tsx:280-443`). No new
table component, no table library.

### 6.1 Default vs optional columns (Screener as the specimen)

Today: 11 columns (`screener/page.tsx:421-433`), which is what runs off the
right edge. Rows must stay single-line because `ROW_HEIGHT` is a fixed
31px, so density is bought by *removing* columns, not by wrapping.

**Default (7)**

| # | Column | Align | Content |
|---|---|---|---|
| 1 | Pair | text | `AI/USDT`; **sticky left** when the region scrolls |
| 2 | Buy venue / ask | text | `binance @ 0.0178` + inline age chip (`badge`, tone per DS §1.7) |
| 3 | Sell venue / bid | text | `mexc @ 0.01972` + inline age chip |
| 4 | Net (bps) | **num** | `+1,063.11`, 600 weight, tone per §5.3; header note `2 dp · exact in Detail` |
| 5 | Liquidity (quote) | num | `656.04`, or the literal `unknown` in `--neu` when `liquidity_unknown` |
| 6 | Networks | text | two `NetworkBadge`s (open/closed/unknown), unchanged |
| 7 | *(action)* | text | `Detail` button; **pinned right** when the region scrolls |

Age moves *into* columns 2 and 3 as inline chips (DS §2.2: "age chips are
badge style inline after the venue name, not a separate column, so the
table does not gain two columns"). That removes two columns and puts
freshness next to the thing it describes — the audit's explicit ask.

**Optional, default off** (a `Columns` popover, persisted per user in
`localStorage` under `arb.table.<page>.columns`): Gross (bps) · Lifetime (s)
· Buy age (separate column) · Sell age (separate column) · Suspect ·
First seen.

Net leads and gross follows (DS §1.8). Gross is not a second line inside
the Net cell — that would break the single-line row height — so gross is an
optional column and is **always** present in the drawer, next to net.

Measured fit (13px table type, `px-3` cells): Pair 96 · Buy 176 · Sell 176 ·
Net 104 · Liquidity 120 · Networks 96 · Detail 80 = **848px**.

**This fit is conditional on §3.2's top bar** and must be re-derived if the
lead chooses the §11.1b sidebar fallback:

| Shell | Content width at 1024 | 848px of columns |
|---|---|---|
| §3.2 top bar | 1024 − 48 gutter = **976px** | fits, no scrolling |
| §11.1b sidebar `w-56` at `≥ lg` | 1024 − 224 − 48 = **752px** | **does not fit** — the region scrolls at 1024 |

Under the fallback, move one more column out of the default set (Networks →
optional, its two badges repeating in the drawer's `Limitations` block) to
bring the default set to 752px, and treat 1024 as a scrolling width. At
1440 both shells fit. At 768 (720px content with the top bar) the region
scrolls either way, with Pair sticky-left and the action pinned right.

`Columns` control: a 26px `Button` labelled `Columns (7)` above the table's
right edge, opening a `--bg-panel` / `--border-strong` popover of checkboxes
(`aria-expanded`, `aria-controls`, Escape closes, focus returns). A
`Reset to defaults` text button at its foot.

### 6.2 The bounded scroll region

The `overflow-x-auto` wrapper in `Table` (`ui.tsx:304`) is not
keyboard-operable — no `tabIndex`, no `role`, no accessible name — so a
keyboard-only user cannot reach columns 5–7 at all. `VirtualTable` has an
`aria-label` but still no `tabIndex` (`ui.tsx:393-397`).

Required:

```
role="region"
aria-label="{Table name} — scrollable table, {n} rows"
tabIndex={0}
class="overflow-x-auto rounded border border-[var(--border)]"
```

- The focus ring comes from `globals.css`'s `[tabindex]:focus-visible` — no
  new CSS.
- Sticky header keeps `--bg-panel` (opaque; a transparent sticky header
  over scrolling rows is unreadable) plus
  `0 1px 0 0 var(--border), 0 4px 8px -4px var(--shadow-color)`.
- Sticky first column: `position: sticky; left: 0`, `--bg-panel` on the
  header cell and `--bg` on body cells (`--bg-raised` on hover, matching
  the row), plus a right rule `1px --border` and the scroll-edge cue
  `inset -12px 0 12px -12px var(--shadow-color)` applied only while
  `scrollLeft > 0`.
- Pinned action column: `position: sticky; right: 0`, same treatment
  mirrored.
- Column reduction (§6.1) is the actual fix. The scroll region is the
  fallback for the widths where 7 columns still do not fit, and it must be
  accessible there rather than merely present.

### 6.3 Row identity and focus preservation during background refresh

Both `Table` and `VirtualTable` key rows by **array index**
(`ui.tsx:323` `key={i}`, `ui.tsx:419` `key={startIdx + i}`). Under a 5s
poll that re-sorts by net bps, React reuses DOM nodes across *different*
rows: focus lands on a different pair's `Detail` button than the one the
user tabbed to, an open drawer can be describing the wrong row, and text
selection jumps.

Required:

```ts
// Table / VirtualTable
rowKey?: (cells: ReactNode[], index: number) => string;
// or, preferred, rows become { key: string; cells: ReactNode[] }[]
```

- Mandatory for any polled table. Screener already has a `rowKey(r)`
  helper (`screener/page.tsx:449`) — thread it through.
- **Focus preservation**: before a data swap, record
  `document.activeElement`'s owning row key and the cell index; after the
  swap, if that key is still present, restore focus to the same cell.
- **When the focused row leaves the payload**: move focus to the §6.2
  scroll region (never to `document.body`, which strands a keyboard user at
  the top of the page) and announce `That row is no longer listed.` via the
  page's existing `aria-live="polite"` channel.
- **Never re-sort under an open drawer or a focused row** without a visible
  affordance. Visual spec: a 26px `Button` `Refresh (3 new)` appears in the
  table's header row and the data freezes until pressed or until focus
  leaves the table. Whether the freeze is automatic or opt-in is
  behavioural — UX owns it (§11.4); the *control* is specified here.
- Row hover stays `--bg-raised`. The current `hover:bg-[var(--bg-panel)]`
  (`ui.tsx:325`, `ui.tsx:421`) is a no-op on a `--bg-panel` table header
  and near-invisible on `--bg` body rows; DS §1.1 assigns hover rows to
  `--bg-raised`.
- Stale rows per DS §1.7, unchanged: `opacity: var(--opacity-stale)` per
  cell, **excluding** the age cell/chip, and hover suspends the dim (the
  raised surface is where the 80% blend drops below AA).

### 6.4 Applied-filter row and the FilterCard arrangement

**Applied-filter row** — between the filter card and the table, 28px tall,
`role="group" aria-label="Applied filters"`:

```
Filters:  [Buy: binance, okx ✕]  [Net ≥ 5 bps ✕]  [Quote: USDT ✕]     Clear all (3)
```

- `Filters:` label 11/14 uppercase `--text-dim`.
- One chip per applied filter *differing from the page default*: 24px,
  `px-2`, 12/16, 1px `--border-strong`, `--text`, plus a 12px `close` glyph
  button with `aria-label="Remove filter: Buy venues"`. Chips wrap; they
  never scroll.
- `Clear all (n)` — a text button, 12/16, `--text-dim`, hover `--text`,
  right-aligned; hidden when `n = 0`.
- When `n = 0` the row still renders, as
  `No filters applied — showing all 7,176 pairs` in `--text-dim`, so the
  table does not jump vertically as filters are added and removed.
- The count is the same `activeCount` `FilterCard` already takes
  (`FilterCard.tsx:16`), defined as every control differing from the page
  default (DS §4.7).

**FilterCard: common first, Advanced disclosed** — the fully expanded form
in `02-screener.png` is what eats viewport 1.

Default (always visible), two rows, card `p-3`, ≤ **120px** tall at
≥ 1024:

1. `Quote asset` select · `Min net spread` (`bps`) · `Min liquidity`
   (`quote`) — `SelectFilterField` / `NumericFilterField`, unchanged.
2. `Buy on` / `Sell on` venue chip groups — `FilterRow` label + chips,
   unchanged.

`Advanced` disclosure — a 26px toggle button, left-aligned under the rows:
`chevron` 16px + `Advanced` 13/18 500 `--text`, `aria-expanded`,
`aria-controls`. Collapsed by default. Carries a count badge
`Badge dim` when any advanced control differs from default, and
auto-expands in that case (a filter that is on must never be hidden).
Contents: `Min lifetime` (`s`) · `Base allow-list` · `Base deny-list` ·
`Include suspect lanes (asset-identity guard)` ·
`Include unknown-liquidity lanes` · the template row (saved-template
select, `Save as template…`, `Reset filters`).

Below `md` the whole card stays behind the existing
`Filters (n active)` toggle (`FilterCard.tsx:24-32`), and `Advanced` nests
inside it.

Invalid input, unchanged from `NumericFilterField`: 1px `--critical`
border + an 11/14 `--critical` message beneath, `aria-invalid`,
`aria-describedby` pointing at the message. A backend rejection replaces
the client message verbatim.

### 6.5 Row identity in a table (the Auto-Paper fix)

A table's identity column never leads with an opaque machine ID.

- Primary text: the human name when the payload has one; otherwise the
  most specific human-readable descriptor available (strategy name, pair,
  venue route).
- The raw ID becomes a **second line**, `mono` 12/16 `--text-dim`,
  middle-truncated to 12 characters with the full value in the drawer's
  `Exact values` block and in `title`. Middle truncation
  (`rule-01M13…27V5VX`) keeps both the prefix and the discriminating tail,
  unlike the current full-width raw ID.
- Because rows are single-line at 31px, the two-line identity applies to
  low-row-count tables only (Auto-Paper's per-rule summary is one row per
  rule). In a high-row-count table the ID lives in the drawer alone.
- When the payload genuinely has no name, the cell shows the truncated ID
  as primary with an `Unnamed rule` `Badge dim` beside it — honest, not a
  fabricated label. Whether the backend can supply names is a backend
  question (§11.5).
- Contextual next step: the identity cell's primary text links to the rule
  (Alerts & Rules), and the row's action column carries
  `Evidence ↗` — the audit's "no prominent next step" finding.

---

## 7. Drawer spec

Extends the existing `RowDrawer` (`web/src/components/RowDrawer.tsx`).

### 7.1 Bounded width

`≥ md`: `width: min(420px, 50vw)`, floor 360px — today's
`md:w-[420px] md:min-w-[360px] md:max-w-[50vw]` is already correct and
stays. At 768 that resolves to 384px; at 1024+ to 420px.

`< md`: full-screen, `--overlay` scrim (both already implemented).

The drawer is `position: fixed`, so it cannot widen the page. Every
horizontal-overflow symptom in `03-screener-detail.png` is *internal* —
§7.2.

### 7.2 The internal overflow fix

Cause: the body's `dl grid-cols-2` (`screener/page.tsx:570`) with an
unbreakable 22-digit decimal in a `<dd>`. A grid item's default
`min-width: auto` refuses to shrink below its content, so the grid widens
and the body scrolls sideways.

Required:

- `min-w-0` on the `<dl>` and on every `<dd>`.
- `overflow-wrap: anywhere` on `<dd>` and on the `Exact values` block.
- Columns become `grid-cols-[minmax(0,1fr)_minmax(0,1fr)]`; the `<dt>`
  keeps `--text-dim` left, the `<dd>` stays right-aligned with
  `tabular-nums`.
- Every number in the body is bounded per §5 (`fmtDp` / `fmtSig`), so the
  22-digit string is not in the layout at all — it is in the
  `Exact values` block, where it wraps.
- The body keeps `overflow-y-auto` and gains **`overflow-x: hidden`**. If
  anything still overflows horizontally it is a bug to fix at the source,
  not to scroll around.

### 7.3 Header: title with age and status

44px, 1px `--border` bottom rule, `px-3`:

- Title 13/18 600, `truncate`, format `AI/USDT · binance → mexc`.
- Age badge (existing `ageBadge` prop) immediately after, `Badge` with the
  DS §1.7 tone and the `STALE 11.2s` word form.
- Any row-level status badge follows: `SUSPECT` (`Badge high`),
  `LIQUIDITY UNKNOWN` (`Badge dim`), `NETWORK CLOSED` (`Badge bad`) — all
  driven by fields `ScreenerSpreadRow` already carries (`suspect`,
  `suspect_reason`, `liquidity_unknown`, `networks`).
- Close button 32×32, 20px `close` glyph, `aria-label="Close detail panel"`
  (unchanged).
- Because the title truncates, the **first body row repeats the identity in
  full** (`Pair`, `Route`) — identity must never exist only in a truncated
  header.

### 7.4 Grouped body

`Section` blocks in this order, 16px padding, 8px row gap, 16px between
blocks:

1. **Identity** — Pair, Route, First seen, Lifetime.
2. **Buy side** — Venue, Ask, Ask qty, Taker fee, Age.
3. **Sell side** — Venue, Bid, Bid qty, Taker fee, Age.
4. **Spread** — Net (bps) *(600 weight, tone per §5.3, leads)*,
   Gross (bps) *(`--text-dim`, same size, never toned by sign — DS §1.8)*,
   Liquidity (quote).
5. **Limitations** — one row per limitation the payload reports, each with
   the backend's verbatim reason: suspect + `suspect_reason`,
   `liquidity_unknown`, network states + `networks.reason`, and the
   no-transfer note. Absent fields render nothing; nothing is inferred.
6. **`Exact values`** — the §5.5 tier-2 disclosure. Collapsed by default,
   `--bg-raised` body, `mono` 12/16, `overflow-wrap: anywhere`, one
   `label: value` line per bounded field, values verbatim.

Fact rows are a 2-column `<dl>`: `<dt>` `label` style `--text-dim` left,
`<dd>` `table` style right-aligned with tabular numerals.

### 7.5 Footer and keyboard contract

Footer 48px, 1px `--border` top rule, right-aligned, 12px gap: primary
`Open in Calculator ↗` (`--accent` 13/18 + 16px `external` glyph),
secondary `Close` (`Button`). At `< md` the footer is 56px and the buttons
are full-width stacked, min 44px tall.

| Requirement | `≥ md` (non-modal) | `< md` (modal) |
|---|---|---|
| `role` / `aria` | `role="complementary"` `aria-label="Detail: …"` (as today) | `role="dialog"` **`aria-modal="true"`** `aria-label="Detail: …"` |
| Scrim | none | `--overlay` (as today) |
| Escape closes | yes (implemented) | yes |
| Focus on open | close button (implemented) | close button |
| Focus return on close | triggering row/button via `returnFocusRef` (implemented) | same |
| Focus containment | **none** — non-modal by design (DS §4.3, UX §9); the table stays reachable. The drawer must be mounted in DOM order immediately after the table so Tab order is natural rather than jumping to the end of the document. | **contained**, same loop as `ConfirmDialog` (`ui.tsx:552-575`) |
| Background inert | no | yes (`aria-hidden` on `main`, or `inert`) |

The `< md` row is a **gap in today's code**: `RowDrawer` renders a scrim
but no `aria-modal` and no trap, so a mobile screen-reader user can tab
behind a visually-blocking panel. Same class of gap as the mobile nav sheet
(§3.7).

Motion: 200ms `translateX` in, `prefers-reduced-motion: reduce` → 0ms (the
component already uses `motion-safe:` prefixes — keep them).

---

## 8. Feasibility-first result spec (Calculator)

`04-calculator.png` is the worst hierarchy failure in the audit: green
`NET` and `NET BPS` figures sit *above and left* of a same-sized card
reading `LIQUIDITY insufficient`, so the layout says "profitable" and the
content says "not executable at this size".

**Returned numbers are never changed.** Every figure renders from
`ScreenerCalculatorResult`'s own strings. What changes is order, weight and
tone.

### 8.1 Region order

```
1  INPUTS            (common fields only; overrides in Advanced)
2  FEASIBILITY       ← the backend's outcome, first, full width
3  ESTIMATE          ← the numbers
4  LIMITATIONS & SOURCE
5  Advanced          (fee overrides, transfer fee — collapsed)
```

### 8.2 Feasibility block

Full width, `--bg-panel`, 1px border in the outcome tone, **3px left bar**
in the outcome tone, radius 4, `p-3`, 16px below Inputs and 16px above
Estimate. No tinted fill (§1.3).

- Heading: 16px glyph (`warn-tri` for warn/bad, `info` for ok/unknown) +
  13/18 600 in the tone. Headings, exactly:
  - failed check → **`Not executable at this size`** (`--warn`; `--critical`
    if the backend ever reports a hard block)
  - all reported checks pass → **`Meets the checks we can see`** (`--ok`)
  - no check reported → **`Feasibility not reported`** (`--text-dim`)
- Body: one row per **reported** check, 13/18 — check name in `label` style
  `--text-dim` left, verdict word in the tone right, then the backend's own
  qualifier:

  ```
  LIQUIDITY      insufficient   at 1,000 USDT
  ```

- **Checks absent from the response render nothing at all.** A check is
  never inferred, and silence is never read as "ok".
- Footer line, 12/16 `--text-dim`:
  `Checks shown: liquidity. Others are not reported by this endpoint.`
- Constant sentence beneath the block, 13/18 `--text-dim`:
  `This is an estimate from the current top of book, not an executable
  quote.` The word "guaranteed" (and "risk-free", "certain", "always",
  "proven") appears nowhere, and the pass heading deliberately says
  "the checks we can see" rather than "executable".

**What the endpoint actually carries today.** `ScreenerCalculatorResult`
(`web/src/lib/api/client.ts:1625-1636`) has exactly one feasibility field:
`liquidity_ok: boolean`. The `stale` and `guard` outcomes named in the brief
**do not exist in this response**. The block is therefore specified as a
slot list with one occupant today:

| Slot | Field | Status |
|---|---|---|
| Liquidity | `liquidity_ok` | exists |
| Book freshness | *(not in the response)* | needs backend — §11.6 |
| Asset-identity guard | *(not in the response)* | needs backend — §11.6 |
| Network open/closed | *(not in the response)* | needs backend — §11.6 |

### 8.3 Estimate block

- **Net leads.** One 18/24 600 figure, `fmtDp(net, 2)` + quote asset code,
  sign per §5.3, with `NET (bps)` beside it at 14/20 600. Everything else
  is a 2-column `<dl>` below at 13/18: Buy ask · Sell bid · Size (base) ·
  Gross · Buy fees · Sell fees · Transfer fee — each bounded per §5.2, each
  with its asset code, none truncated (§5.6 stacking replaces `Stat`'s
  `truncate`).
- **Tone suppression — the actual fix.** When any reported check fails, the
  net figure renders in **`--text`, not `--pos`**, and the block heading
  carries a `Badge warn` `NOT EXECUTABLE AT THIS SIZE`. Colour must not say
  "good" while the feasibility block says "no". The digits are byte-for-byte
  the backend's; only the tone changes.
- When all reported checks pass, net keeps §5.3's signed tone
  (`--pos`/`--neg`/`--neu`).
- `Exact values` disclosure (§5.5 tier 2/3) at the foot of the block: ≤ 9
  figures, so each also carries the `sr-only` exact suffix.

### 8.4 Limitations & source

- When the user arrived via the drawer's `Open in Calculator ↗`: a
  provenance line, 12/16 `--text-dim`:
  `From Screener row AI/USDT binance → mexc, first seen 2026-09-12
  13:05:45Z`, followed by the **originating row's** own badges — `SUSPECT`,
  `LIQUIDITY UNKNOWN`, network states — each labelled as coming from that
  row, visually separated from the calculator's own output by a 1px
  `--border` rule and a `From the Screener row` sub-label.
  `ScreenerSpreadRow` carries all of these (`suspect`, `suspect_reason`,
  `liquidity_unknown`, `networks.buy_withdraw`, `networks.sell_deposit`,
  `networks.reason`) — strictly richer feasibility data than the calculator
  response, and the handoff currently drops it. Whether the handoff can
  carry it is §11.6.
- Always: the no-transfer-model note and the `Advanced` override notice
  (`Fees overridden: buy 10 bps, sell 5 bps` in `--warn` whenever an
  override is set, so an estimate produced from non-default fees never
  looks like a default one).

### 8.5 Inputs and Advanced

Row 1: Base asset · Quote asset. Row 2: Buy venue · Sell venue. Row 3:
Size (quote) with the quote asset code as the in-field unit suffix
(`NumericFilterField`'s existing pattern). `Calculate` — 26px `Button`,
left-aligned under row 3.

`Advanced` disclosure (same control as §6.4): Transfer fee (quote),
Override buy fee (bps), Override sell fee (bps). Collapsed by default,
auto-expanded with a count badge when any override is set.

---

## 9. Component states

One shared mapping, because every state already has an owner primitive in
`ui.tsx`. Only genuine per-component deltas follow.

### 9.1 Shared state table

| State | Primitive | Visual |
|---|---|---|
| loading | `Loading what=…` / `Await` | `Loading {what}…` 14/20 `--text-dim`. Region keeps its heading and its height where known. No skeleton shimmer, no placeholder digits. `aria-busy="true"` on the region. |
| empty (healthy) | `Empty what=…` | `No {what}.` 14/20 `--text-dim`, plus a sentence saying *why the absence is normal* and the next step. **Never names an environment variable or a doc path when the component is healthy** — Paper Trading's current `Persistence needs a database connection — see docs/deployment.md` is wrong when the Overview strip says `DATABASE CONNECTED`, and is the audit's finding 5. Copy is UX's; the rule is: an empty state may only assert a fault the payload actually reports. |
| stale | `DataAge` + DS §1.7 | `STALE — Updated 41s ago` in `--warn`; per-cell `opacity: var(--opacity-stale)` excluding the age cell; hover suspends the dim. Drawer bodies are **not** dimmed (DS §4.3) — the header badge is enough. |
| error (transient, last-good held) | `Await` | `Refresh failed ({message}) — showing the last good data.` 1px `--warn` border, `role="alert"`, the stale payload still rendered beneath with its `DataAge`. Never blank a table on a failed refresh. |
| error (nothing held) | `ErrorBox` | 1px `--critical` border on `--bg-panel`, `p-4`, `Error:` in `--critical` 500 + the backend's verbatim message. |
| **401** | `ErrorBox status={401}` | prefix `Session required:`; add a `Sign in` link (`--accent`) — a 401 has exactly one next action. |
| **403** | `ErrorBox status={403}` | prefix `Forbidden:` + the backend's message. No retry affordance (retrying will not help). |
| **permission-denied, pre-emptive** | `GatedControl state="role"` | `--text-dim` at `opacity-disabled` (0.4), `cursor-not-allowed`, `aria-disabled`, `title` naming the required role verbatim (`Requires OPERATOR or ADMIN`). Used *instead of* letting a user click into a 403. |
| **entitlement-gated** | `GatedControl state="package"` | solid `--text-gated` label at full opacity, leading icon at 0.6, 12px `lock` glyph with `aria-label="Included in {Package} — upgrade"`, `Badge dim` package name, 1px `--border-strong`, whole control links to `upgradeHref`. Never `pointer-events: none`. Never rendered in the operator console. |
| **stale setting version (409)** | `StaleVersionNotice` | 1px `--warn` border, `Someone applied version {n} while you were editing — reload to continue.` + `Reload` `Button`. The draft is never silently reconciled or re-sent. |
| absent component (404 + absence code) | `Unavailable code=…` | `ABSENCE_CODES` copy (`ui.tsx:72-81`) + the `docs/deployment.md` pointer. `ErrorBox` already routes these (`ui.tsx:140-141`). |

### 9.2 Per-component deltas only

| Component | Deltas |
|---|---|
| `StatusStrip` | loading → every segment `state="loading"`, label + literal `…`, roll-up word `…`, `aria-busy`. A single failing upstream degrades **one segment** to `unknown` with the verbatim reason — never blanks the strip (the existing `statOrError` degrade-one-cell pattern). 401/403 on the strip's own source → that segment goes `unknown`, reason `Sign-in required` / `Not permitted`; the strip never renders an `ErrorBox` (it is chrome). |
| Table | empty → `Empty` inside the bordered region so the region's height and header survive; the empty string keeps the existing "0 of N pairs qualify" shape, which tells a user the filters are the cause. Error with last-good → the `Await` warn banner *above* the region, data still shown. |
| Filter card / applied-filter row | never loading or error; when the venue list fails to load, the chip group renders the last-known venues with a `Badge warn` `venue list may be out of date`, and unknown venues take the `unbuilt` treatment with the verbatim backend reason. |
| `RowDrawer` | loading → header renders immediately from the row already in hand, body shows `Loading detail…`; error → `ErrorBox` **inside** the body, header and footer stay (so Close and Open in Calculator still work); stale → header badge carries `STALE {age}`, body not dimmed. |
| Calculator | pre-submit → Estimate and Feasibility blocks absent entirely (no zeroed placeholder result); submitting → `Calculate` becomes `Calculating…` and disables, previous result stays visible dimmed at `--opacity-stale` with a `previous result` `Badge dim`; error → `ErrorBox` in place of the Feasibility block, Estimate cleared (never a stale estimate under a fresh error); `liquidity_ok = false` → §8.2 warn + §8.3 tone suppression. |
| Nav chrome | mode loading → `Mode…` `--text-dim`; mode error → `Mode unknown` `--text-dim` (never an `ErrorBox` in chrome — today's behaviour, kept). `PaperControl` returns `null` when the profile has no paper engine; a `VIEWER` sees the state text with no button. |
| Destructive region | the action is `GatedControl state="role"` for a non-ADMIN, and `Button disabled` with the precondition sentence beside it (`pause the engine before resetting`) when a precondition fails — two different states, two different treatments, never one grey button. |

---

## 10. Accessibility

Target: **WCAG 2.2 AA**.

### 10.1 Contrast

Method is DS §0 (WCAG 2.x relative luminance; `(L_light + 0.05) /
(L_dark + 0.05)`; ≥ 4.5:1 text, ≥ 3:1 UI boundaries and focus indicators).

**This spec introduces no colour pair absent from DS §1.3**, so it adds no
unverified ratio. The pairs it relies on, quoted from DS §1.3:

**Dark** (`--bg` #0b0e14 · `--bg-panel` #11151f · `--bg-raised` #171c29)

| Foreground | on `--bg` | on `--bg-panel` | on `--bg-raised` | Used by |
|---|---|---|---|---|
| `--text` | 14.06 | 13.28 | 12.38 | all numerals, nav current label, strip word (suppressed tone, §8.3) |
| `--text-dim` | 7.11 | 6.71 | 6.26 | labels, column headers, units, breadcrumb, helper copy |
| `--text-dim` @ 0.8 | 4.88 | 4.74 | 4.4 → **do not stale-dim on raised**; hover suspends the dim | stale cells |
| `--text-gated` | 5.74 | 5.43 | 5.06 | package-gated labels |
| `--accent` | 6.00 | 5.67 | 5.29 | links, current-tab rule, focus ring |
| `--ok` | 8.11 | 7.66 | 7.14 | `ok` strip word, `--pos` figures |
| `--warn` | 8.83 | 8.34 | 7.77 | `warn` strip word, feasibility heading |
| `--high` | 6.56 | 6.19 | 5.77 | `SUSPECT` badge |
| `--critical` | 5.79 | 5.47 | 5.10 | `critical` strip word, `--neg` figures, error text |
| `--border` | 1.35 | 1.27 | 1.1 | decorative rules only — never a control's sole boundary |
| `--border-strong` | 3.56 | 3.36 | 3.14 | chips, inputs, badges, buttons, drawer edge, popovers |
| `--on-accent` on `--accent` fill | 6.00 | | | primary button, selected chip |
| `--on-critical` on `--critical` fill | 5.79 | | | `Button danger` hover only |

**Light** (`--bg` #f5f6f8 · `--bg-panel` #ffffff · `--bg-raised` #eceef2)

| Foreground | on `--bg` | on `--bg-panel` | on `--bg-raised` | |
|---|---|---|---|---|
| `--text` | 16.10 | 17.41 | 14.99 | |
| `--text-dim` | 7.47 | 8.08 | 6.95 | |
| `--text-dim` @ 0.8 | 4.52 | 4.76 | 4.28 → not on raised | |
| `--text-gated` | 4.71 | 5.09 | 4.38 → never place gated on raised | |
| `--accent` | 4.94 | 5.34 | 4.60 | |
| `--ok` | 4.97 | 5.38 | 4.63 | |
| `--warn` | 5.90 | 6.38 | 5.49 | |
| `--high` | 5.71 | 6.17 | 5.31 | |
| `--critical` | 4.89 | 5.29 | 4.55 | |
| `--border` | 1.28 | 1.39 | 1.2 | decorative only |
| `--border-strong` | 3.34 | 3.61 | 3.11 | |
| `--on-accent` (#fff) on `--accent` | 5.34 | | | |
| `--on-critical` (#fff) on `--critical` | 5.29 | | | black on `--critical` would be 3.97 — the reason `text-black` must not return |

Consequences this spec depends on:

- The strip's `unknown` state uses `--text-dim` (6.26–7.47) and is
  distinguished from `ok` by a **hollow dot and the word**, not by hue.
- Nav current-state uses `--bg-raised` + 600 weight + an `--accent` rule
  (≥ 4.60 as text, ≥ 3:1 as a boundary) + `aria-current` — four carriers,
  §3.5.
- The drawer's identifying edge is `--border-strong` (3.14–3.61), not the
  shadow.
- `--shadow-color` carries no text and is never a sole boundary, so it has
  no ratio to meet.
- No `--opacity-stale` dimming of a `--text-dim` cell on `--bg-raised`
  (4.4 dark / 4.28 light) — hover suspends the dim (DS §1.7).

DS §0 asks the frontend for a unit test recomputing every **must** ratio
from the live `globals.css`. This spec adds no fixture; it relies on that
test. Because there is no `scripts/check-contrast.mjs` in the repo today,
the fixture list above is currently design-owned expectation, not a
machine-verified fact — worth knowing before ship.

### 10.2 Focus

- Visible focus is `globals.css`'s existing `outline: 2px solid
  var(--accent); outline-offset: 2px` on `a`, `button`, `input`, `select`,
  `textarea`, `[tabindex]`. No component overrides it, and no component
  sets `outline: none` without an equivalent replacement.
- New focusables introduced here — the §6.2 scroll region, the §3.3/§3.4
  nav overflow regions, the `Columns` popover trigger, the `Advanced` and
  secondary-section disclosures, the applied-filter chip `✕` buttons, the
  `Exact values` disclosure — all rely on that selector. The scroll region
  works because of the `[tabindex]:focus-visible` clause.
- The ring must not be clipped: containers with `overflow: hidden`
  (`StatusStrip`, the table region) give focusable children 2px of inner
  padding, or use `outline-offset: -2px` (inset ring) where padding is not
  available.
- Focus order is DOM order. The sticky nav bars are first in the DOM; a
  `Skip to main content` link (`sr-only`, becoming visible on focus,
  `--accent` on `--bg-panel`, top-left) precedes them.
- Focus return is specified per component: drawer → triggering row (§7.5),
  dialog → opener (`ui.tsx:544-550`), nav sheet → hamburger, popover →
  trigger, disclosure → the toggle.

### 10.3 Reduced motion

- Total motion in this refinement: drawer translate 200ms · disclosure and
  secondary-section height 150ms ease-out · chevron rotate 150ms · nav
  sheet slide 200ms · hover/focus colour transitions 120ms.
- `@media (prefers-reduced-motion: reduce)` → **all of the above are 0ms**.
  State changes still happen, instantly. Use Tailwind's `motion-safe:`
  prefix, as `RowDrawer` already does.
- No auto-playing motion exists anywhere: no shimmer skeleton, no spinner,
  no marquee, no pulsing status dot, no animated count-up on a figure. A
  polling console that animates its own refresh is unreadable, and a
  pulsing dot would put state in an animation channel.
- Live data still updates under reduced motion — reduced motion governs
  transitions, not content.

### 10.4 Labelling rules

- Every icon-only control has an `aria-label` on the **control**, never on
  the `<svg>`; every `<svg>` is `aria-hidden="true"` (DS §3). `title` is
  never an accessible name.
- Landmarks: one `<header>` (nav chrome), `<nav aria-label="Primary">`,
  `<nav aria-label="{Destination} sections">`, `<nav aria-label="Breadcrumb">`,
  one `<main>`. One `<h1>` per page (`PageTitle`); `Section` headings are
  `<h2>`; drawer block headings are `<h3>` inside the drawer's labelled
  region.
- `StatusStrip` is `role="group"` with an `aria-label`, and its `sr-only`
  first child mirrors the roll-up so the summary is announced before the
  segments.
- Table: `<th scope="col">` on every header (already), a `<caption>` or
  `aria-label` naming the table and its row count, and `aria-sort` on the
  sorted column's header.
- Disclosures: `aria-expanded` + `aria-controls` on the toggle; the count
  is in the toggle's visible text (`Advanced (2)`, `Diagnostics (9)`), not
  only in a badge colour.
- Filter chips: `aria-pressed` on a toggle chip (as `ChipGroup` already
  does) and `aria-label="Remove filter: {name}"` on an applied-filter `✕`.
- Numbers: units and asset codes are in visible text, not implied by
  position. The §5.1 rule 6 less-than forms carry an `sr-only` expansion so
  `<` and `>-` are never the only carrier.
- Status words are real text, never `::before` content or a
  background-image, so they survive high-contrast and custom-stylesheet
  modes.
- `aria-live="polite"` regions: the notification bell's count, the
  "row no longer listed" announcement (§6.3), and toast output. Nothing
  polled announces itself on every tick — the strip and tables are not live
  regions.

### 10.5 Out of scope here (owned elsewhere, listed so nothing reads as dropped)

- **Chart palettes for signed values** — DS §5.1/§5.2/§5.3 own them
  (`--accent` primary line; `--pos` fill; `--neg` fill at 0.18 **plus a
  dashed `4 3` outline** as the deuteranopia-safe carrier; `--neu` and a
  `--border-strong` zero line; second series `--warn` dotted; max 5
  categorical series). No new chart dependency, no new palette. This
  refinement adds no chart.
- **Icon guidelines** — DS §3 owns them: inline SVG only,
  `viewBox="0 0 16 16"`, `fill="none" stroke="currentColor"`,
  `aria-hidden="true"`, two rendered sizes (16 @ strokeWidth 1.3, 20 @ 1.5),
  1.5px inset, primitives only, `currentColor` always. Two glyphs this
  refinement needs, in the 16 viewBox, drawn to match:
  `columns` = `<rect x="2" y="3" width="12" height="10" rx="1"/>` +
  `<path d="M6.5 3v10M10 3v10"/>`;
  `filter` = `<path d="M2.5 3.5h11L9.5 8.5v4.5l-3-1.5V8.5Z"/>`.
  Existing glyphs cover the rest (`ChevronIcon`, `CloseIcon`,
  `ExternalIcon`, `CheckIcon`, `PlusMinusIcon`, `InfoIcon`, `WarnTriIcon`,
  `LockIcon`, `BellIcon`).
- **Marketing-site style guide** — DS §6 owns it, and it stays consistent
  with the console because it imports the same `globals.css`: light default,
  1120px max width, 12-column grid, `display` 40/48 down to `caption`
  12/16, primary button `--accent`/`--on-accent` 40px, real console
  screenshots with the mode banner uncropped, every figure carrying its `n`
  and a `Source: docs/campaigns/…` citation, and the verbatim
  *"Simulated on paper. Past paper performance is not a projection or
  guarantee of future results."* footer. One addition from §1.2 applies
  there too: `--shadow-color` replaces any hardcoded screenshot shadow.

---

## 11. Conflicts and decisions the lead must resolve

### 11.1a Icon rail removal contradicts two published specs

UX §2.1 and DS §4.1 both specify the icon rail (44px rail + 176px label
column, per-group glyphs, collapse toggle, tooltip-on-focus). §3.1 removes
it. DS §4.1's rail button state table and DS §3's six `group-*` glyphs
become dead spec if this is accepted. I cannot edit DS (single-file scope),
so the lead needs to decide whether DS §4.1 is amended, marked superseded
for the client area, or whether the rail stays. **Recommendation: remove**
(four reasons in §3.1, the strongest being that with six destinations there
is no group layer left for a rail to address).

This is the decision the brief authorised ("the redundant simultaneous group
icon rail should be removed unless you can argue a clear need"). §11.1b is
the larger decision it does **not** cover, and they should be taken
separately.

### 11.1b Sidebar → top bar: a structural change, and §6.1 depends on it

§3.2 replaces the 224px left sidebar (`ConsoleShell.tsx:847`, `w-56`) with a
48px sticky top bar plus a 36px secondary tab rail. That is a change to the
shell's axis, not a restyle, and it goes beyond removing the rail. It is
specified as primary because it is **load-bearing for §6.1**: the 7-column
default Screener set is 848px, which fits 1024 only if navigation stops
consuming 224px of every viewport. It also relocates the mode banner, which
UX §2.1 pins "full-width under the brand row" — §3.2 keeps it full-width but
moves it below the secondary bar.

**Fallback, if the lead prefers minimal churn to `ConsoleShell`:** keep a
left sidebar, reduced to **208px**, holding the six destinations as 36px
rows (current state per §3.5, with the `--accent` bar on the *left* edge
rather than a bottom rule); show it at **`≥ lg` only** (not `md`, which
leaves 496px of content at 768); below `lg` the hamburger sheet is the
primary nav; secondary navigation becomes a page-level tab rail
(§3.4's geometry, rendered inside `main` under the breadcrumb rather than in
the chrome); the mode band and the persistent mode chip + pause control move
into a 44px page-level bar at the top of `main`, so §3.6's every-width
guarantee still holds. Under the fallback, §6.1's column table applies and
one more column leaves the default set.

Everything else in this document is shell-agnostic: §4, §5, §6.2–§6.5, §7,
§8, §9 and §10 are unaffected by which branch is chosen.

### 11.2 Six destinations: labels and mapping are UX's

§3.3 uses the audit's recommended labels (Overview, Discover, Paper Trading,
Research & Results, Alerts & Rules, Settings) as placeholders. UX §2.2's
existing operator group set (Operate, Scanner Suite, Portfolio, Research,
Control, System + pinned Settings) is a *different* cut. The visual spec
does not depend on which labels win — six 36px items in a 48px bar plus a
secondary rail — but the two documents must not ship with different sets.

**Label width is the tightest constraint on §3.2 and must be measured before
the set is frozen.** The six placeholders sum to roughly 660px at 13/18 with
`px-3` and 4px gaps; plus ~100px of brand and ~210px of right cluster (mode
chip, pause button, bell, theme) that is ≈ 970px, fitting 1024 with about
50px of margin. `Research & Results` is the long pole. A set two words
longer pushes the bar into its §3.3 horizontal-scroll fallback at 1024,
which is a worse outcome than shorter labels — so if UX wants longer names,
the longer name belongs on the *secondary* rail, not the primary bar.

### 11.3 Name collision: "stats strip" vs "status strip"

UX §2.3 already calls a row of `Stat` **figures** a "stats strip". §4's new
component shows **states**. Two nearly identical names for two different
components will be mis-wired. Proposal: `StatusStrip` (states, §4) and
`SummaryRow` (figures, UX §2.3 renamed).

### 11.4 Sort/refresh freeze policy is behavioural

§6.3 specifies the `Refresh (3 new)` control and the focus-restoration
contract. Whether a polled table freezes automatically while a row is
focused or a drawer is open, or only on explicit opt-in, is a UX decision
with a QA consequence. The visual affordance is specified either way.

### 11.5 Auto-Paper human-readable rule names

§6.5 requires a human name as the primary identifier, with the ULID
demoted. Whether the rule payload can carry a name (and whether existing
rules have one) is a backend question. If it cannot, the fallback in §6.5
(middle-truncated ID + `Unnamed rule` badge) ships — but a
`Name your rule` field is the better product answer.

### 11.6 Calculator feasibility fields do not exist yet

This is the one place where the brief's item 8 and the API disagree.
`ScreenerCalculatorResult` (`client.ts:1625-1636`) carries **only**
`liquidity_ok: boolean` — no book-freshness field, no asset-identity guard,
no network state. §8.2 is therefore specified to render only what the
response carries, with a footer naming which checks were shown. Two asks:

1. **Backend**: add the freshness / guard / network outcomes to the
   calculator response, as named booleans or verdicts with verbatim
   reasons. Until then the UI must not imply the checks were run.
2. **Handoff** (buildable today, no backend change): the drawer's
   `Open in Calculator ↗` currently drops the originating row's `suspect`,
   `suspect_reason`, `liquidity_unknown` and `networks.*` — fields
   `ScreenerSpreadRow` already has and the calculator response does not.
   §8.4 renders them as **labelled provenance from the Screener row**,
   clearly separated from the calculator's own output. That needs the
   handoff to carry them (query params or client state), which is a
   frontend change I am flagging rather than specifying, since it touches
   the route contract UX owns.

### 11.7 Defects found while writing this spec

Design-owned expectations that the current code contradicts. Listed for the
frontend engineer and QA, not fixed here.

| Location | Defect | Spec |
|---|---|---|
| `web/src/lib/decimal.ts:98-101` | `signTone("0")` returns `"ok"` — a true zero renders green | DS §1.8: zero → `--neu` |
| `web/src/app/screener/page.tsx:470` | `netTone === "ok" ? --pos : --neg` — same zero-is-green bug, second call site | §5.3 |
| `web/src/components/RowDrawer.tsx:64` | dark shadow `rgba(0,0,0,.25)` hardcoded in both themes | §1.2 `--shadow-color`; DS §4.3 already specified two values |
| `web/src/components/ui.tsx:304` | `overflow-x-auto` wrapper has no `tabIndex`/`role`/`aria-label` — columns past the fold are unreachable by keyboard (WCAG 2.1.1) | §6.2 |
| `web/src/components/ui.tsx:393-397` | `VirtualTable` region has `aria-label` but no `tabIndex` | §6.2 |
| `web/src/components/ui.tsx:323, 419` | rows keyed by array index — row identity and focus are destroyed by a re-sorting poll | §6.3 |
| `web/src/components/ui.tsx:325, 421` | row hover is `--bg-panel`, a near no-op | DS §1.1: hover rows are `--bg-raised` |
| `web/src/components/RowDrawer.tsx:57-64` | `< md` drawer has a scrim but no `aria-modal` and no focus containment | §7.5 |
| `web/src/components/ConsoleShell.tsx:820-845` | mobile nav sheet has a scrim but no `aria-modal` and no focus containment | §3.7 |
| `web/src/app/screener/page.tsx:570` | drawer `dl grid-cols-2` without `min-w-0` — the horizontal overflow in `03-screener-detail.png` | §7.2 |
| `web/src/components/ui.tsx:59` | `Stat` value is `truncate` — the ellipsised Calculator figures and the cut drawdown | §5.6 stacking |

---

## 12. Build checklist

1. `globals.css`: add `--shadow-color` to **all three** blocks (§1.2). No
   other token change.
2. `format.ts`: `fmtDp` / `fmtSig` / `fmtCount` per §5.1, string-only, plus
   the §5.3 composition note at every call site.
3. `decimal.ts`: `signTone` gains a zero branch (§11.7).
4. `ui.tsx`: `Table`/`VirtualTable` gain `rowKey`, `role="region"`,
   `tabIndex={0}`, `aria-label`, sticky first/last column support, the
   `--bg-raised` hover fix, and `Stat` drops `truncate` in favour of §5.6
   stacking plus the `sr-only` exact suffix.
5. New `StatusStrip` per §4. New `AppliedFilters` row per §6.4. New
   `Disclosure` (used by Advanced, secondary sections, `Exact values`,
   `Columns`).
6. Nav chrome per §3: top bar, secondary tab rail, breadcrumb, mode band,
   persistent mode chip + pause control at every width; `IconRail` removed
   (pending §11.1a). **Do not start this item before the lead answers
   §11.1b** — the sidebar-vs-top-bar branch changes §6.1's default column
   set, so items 4/6 must be settled together.
7. `RowDrawer` per §7: `min-w-0`, `overflow-wrap`, `overflow-x: hidden`,
   grouped body, `Exact values`, `aria-modal` + focus containment below
   `md`.
8. Calculator per §8: region order, feasibility block, net-leads estimate,
   tone suppression on a failed check, Advanced overrides.
9. Settings per §2.6. Destructive regions moved last per §2.5.
10. QA gates: the §2.4 above-the-fold budget at 1440/1024/768/390 and at
    200% zoom; no page-wide horizontal scrollbar at any of them; every
    §11.7 defect closed; keyboard-only traversal of every table's full
    column set; `prefers-reduced-motion` produces zero transitions.
    `web/e2e/console.spec.ts` contains no reference to `IconRail`, a rail
    selector or a group `aria-label` (grepped 2026-09-12), so removing the
    rail should not break it — re-verify at build time rather than assume.
