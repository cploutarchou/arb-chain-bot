# Console design system — tokens, type, icons, components, charts, marketing

Status: DRAFT 2026-08-27. Author: UI design (design-only; no application code).
Source of truth once applied: `web/src/app/globals.css` (tokens) and
`web/src/components/ui.tsx` (primitives). This document is the spec those two
files implement; where they disagree with it after the token changes in §1.4
land, the code is wrong.

Companion documents: `docs/design/ux/console-v2.md` (behaviour, copy, page
layouts — referenced as "UX §n" below), `docs/design/console-ux-audit.md`
(status vocabulary, density, a11y baseline), `docs/design/scanner-suite.md`
§5 (light theme commitment).

Platform non-negotiables this document inherits unchanged: money is decimal
strings rendered verbatim (no colour or chart ever recomputes a number),
live trading stays disabled until the production gate, nothing is ever
described as guaranteed, every glyph/colour/word here is our own, and any
number shown on a marketing page traces to `docs/campaigns/`.

---

## 0. Contrast method (so every ratio below is reproducible)

- WCAG 2.x relative luminance: each sRGB channel `c/255`, linearised as
  `c ≤ 0.03928 ? c/12.92 : ((c+0.055)/1.055)^2.4`; `L = 0.2126R + 0.7152G +
  0.0722B`; ratio `(L_light + 0.05) / (L_dark + 0.05)`.
- Targets: **≥ 4.5:1** for text < 18.66px regular / < 14px bold (which is
  every text size in the console — §2), **≥ 3:1** for UI component
  boundaries and focus indicators (WCAG 1.4.11), **≥ 3:1** for large text.
- Disabled controls (WCAG 1.4.3 exemption) are the *only* things allowed
  below 4.5:1, and only the `unbuilt` / `role` states qualify (§1.6). The
  `package` state is a clickable sales surface and must meet 4.5:1.
- Semi-transparent text is checked at its *effective* colour (sRGB blend
  over the surface it sits on), which is why §1.6 defines solid tokens for
  the gated state instead of leaning on element opacity.
- Frontend: please add a tiny unit test that recomputes every ratio marked
  **must** in §1.3 from the live `globals.css` values, so a future token edit
  cannot silently regress AA. Design does not own that test; it does own the
  expected values.

---

## 1. Colour tokens

### 1.1 Semantic layers (both themes carry the full set)

| Token | Meaning | Where it is allowed |
|---|---|---|
| `--bg` | page canvas | `<body>`, main column, table body |
| `--bg-panel` | primary surface | sidebar, cards, table header, drawer, dialog, filter card |
| `--bg-raised` | secondary surface / selection | hover rows, active nav item, code chips, chip "on" state, heatmap zero cell |
| `--border` | hairline separators (decorative) | table row rules, section rules, card edges |
| `--border-strong` **(new)** | boundaries that identify a control | inputs, selects, filter chips, badges, buttons, drawer edge, focus-adjacent outlines |
| `--text` | primary text | body, numbers, active labels |
| `--text-dim` | secondary text | column headers, stat labels, help text, unselected nav |
| `--text-gated` **(new)** | package-gated label text (UX §2.4 "lighter than disabled, still readable") | `GatedControl state="package"` only |
| `--accent` | interactive / brand | links, primary button, focus ring, cumulative-PnL line, RECORD mode |
| `--on-accent` **(new)** | ink on an `--accent` fill | primary button label, selected chip label |
| `--ok` / `--warn` / `--high` / `--critical` | status tones (existing `Tone` = `ok`/`warn`/`high`/`bad`/`dim`) | text + outline of `Badge`, `Stat`, banners; never a fill except danger-hover |
| `--on-critical` **(new)** | ink on a `--critical` fill | `Button danger` hover state (today hardcodes `text-black`, which fails AA in light) |
| `--pos` / `--neg` / `--neu` **(new aliases)** | signed-value colours: positive / negative / zero-or-n/a | net spread, carry APR, PnL, calculator net result, chart series |
| `--heat-n4…n1`, `--heat-p1…p4`, `--heat-ink` **(new)** | funding heatmap diverging ramp + ink for the bright end | `FundingHistoryChart` heatmap variant, funding tables |
| `--overlay` **(new)** | scrim behind dialogs/drawer on mobile | `ConfirmDialog`, mobile nav, `RowDrawer` < md |
| `--opacity-disabled` `0.4`, `--opacity-stale` `0.8` **(new numeric)** | the two element-opacity states the UX spec fixes | `GatedControl` unbuilt/role; stale-row cells (§1.7) |

`--high` is now used: it is the `HIGH` severity in `severityTone` and the
`stopped (maintenance margin)`-adjacent "needs attention, not broken"
tone. Its light value changes below so it passes on all three surfaces.

### 1.2 Values

**Light** (`:root`, default) and **Dark** (`:root[data-theme="dark"]` and the
`prefers-color-scheme: dark` block — both blocks must carry the identical
full set, as today).

| Token | Light | Dark | Change vs today |
|---|---|---|---|
| `--bg` | `#f5f6f8` | `#0b0e14` | — |
| `--bg-panel` | `#ffffff` | `#11151f` | — |
| `--bg-raised` | `#eceef2` | `#171c29` | — |
| `--border` | `#d7dbe3` | `#232a3b` | — |
| `--border-strong` | `#7f8797` | `#616a7e` | **new** |
| `--text` | `#171a21` | `#d7dce7` | — |
| `--text-dim` | `#4a505e` | `#959db0` | **changed** (light was `#5b6270`, dark was `#8b93a7`) |
| `--text-gated` | `#6b6e78` | `#888c97` | **new** |
| `--accent` | `#2f66d0` | `#4f8cff` | — |
| `--on-accent` | `#ffffff` | `#0b0e14` | **new** |
| `--ok` | `#177a45` | `#2fbf71` | **light changed** (was `#1f8f52`) |
| `--warn` | `#8a5208` | `#e6a23c` | **light changed** (was `#a8650f`) |
| `--high` | `#a2451a` | `#f0713a` | **light changed** (was `#b8541f`) |
| `--critical` | `#c8323a` | `#ef5a5e` | **dark changed** (was `#e5484d`) |
| `--on-critical` | `#ffffff` | `#0b0e14` | **new** |
| `--pos` | `var(--ok)` | `var(--ok)` | **new alias** |
| `--neg` | `var(--critical)` | `var(--critical)` | **new alias** |
| `--neu` | `var(--text-dim)` | `var(--text-dim)` | **new alias** |
| `--heat-n4` (most negative) | `#2f66d0` | `#8ab4ff` | **new** |
| `--heat-n3` | `#5a8ee8` | `#3d7be6` | **new** |
| `--heat-n2` | `#a9c4f5` | `#1f3f7f` | **new** |
| `--heat-n1` | `#dbe7fb` | `#16294f` | **new** |
| `--heat-0` | `var(--bg-raised)` | `var(--bg-raised)` | **new alias** |
| `--heat-p1` | `#fbe9d2` | `#4a2a0c` | **new** |
| `--heat-p2` | `#f3c98d` | `#7a4610` | **new** |
| `--heat-p3` | `#d9932c` | `#b8741c` | **new** |
| `--heat-p4` (most positive) | `#a86414` | `#f0b04e` | **new** |
| `--heat-ink` | `#ffffff` | `#0b0e14` | **new** — text on `n4`/`p4` (light) and `n3`/`n4`/`p3`/`p4` (dark); every other cell uses `--text` |
| `--overlay` | `rgba(0,0,0,.6)` | `rgba(0,0,0,.6)` | **new** (matches today's hardcoded `bg-black/60`) |
| `--opacity-disabled` | `0.4` | `0.4` | **new** |
| `--opacity-stale` | `0.8` | `0.8` | **new** |
| `color-scheme` | `light` | `dark` | — |

Why the four light tones moved: the light set shipped as a visual match of
the dark set, and mid-tone greens/ambers on white are exactly where a
"looks about right" pick fails — old light `--ok` was 4.11:1 on white, old
light `--warn` 4.28:1 on `--bg`, old light `--high` 4.49:1 on `--bg`. Why
dark `--critical` moved: `#e5484d` was 4.35:1 on `--bg-raised`, and critical
badges sit on raised (hover rows, selected rows). Why `--text-dim` moved in
both: stale rows render dim cells at 80 % opacity (UX §3), and the old
values dropped to 4.25:1 (dark) / 3.90:1 (light) at that blend.

### 1.3 Contrast table (text tokens × surfaces)

Ratios are for the values in §1.2 (i.e. *after* the change). Bold = must
hold (tested). Threshold for text is 4.5, for boundaries 3.0.

**Dark theme**

| Foreground | on `--bg` | on `--bg-panel` | on `--bg-raised` | Verdict |
|---|---|---|---|---|
| `--text` #d7dce7 | **14.06** | **13.28** | **12.38** | AA/AAA |
| `--text-dim` #959db0 | **7.11** | **6.71** | **6.26** | AA |
| `--text-dim` @ 0.8 (stale cells) | **4.88** | **4.74** | 4.4 (do not stale-dim on raised) | AA on bg/panel only |
| `--text-gated` #888c97 | **5.74** | **5.43** | **5.06** | AA |
| `--accent` #4f8cff | **6.00** | **5.67** | 5.29 | AA (text + focus ring ≥3) |
| `--ok` #2fbf71 | **8.11** | **7.66** | 7.14 | AA |
| `--warn` #e6a23c | **8.83** | **8.34** | 7.77 | AA |
| `--high` #f0713a | **6.56** | **6.19** | 5.77 | AA |
| `--critical` #ef5a5e | **5.79** | **5.47** | **5.10** | AA (old value: 4.94 / 4.66 / 4.35) |
| `--border` #232a3b | 1.35 | 1.27 | 1.1 | decorative only — never the sole boundary of a control |
| `--border-strong` #616a7e | **3.56** | **3.36** | **3.14** | ≥3 boundary on all surfaces |
| `--on-accent` #0b0e14 on `--accent` fill | **6.00** | | | AA |
| `--on-critical` #0b0e14 on `--critical` fill | **5.79** | | | AA |
| `--text-dim` @ 0.4 (disabled) | 1.97 | | | exempt (inactive control) |

**Light theme**

| Foreground | on `--bg` | on `--bg-panel` | on `--bg-raised` | Verdict |
|---|---|---|---|---|
| `--text` #171a21 | **16.10** | **17.41** | **14.99** | AA/AAA |
| `--text-dim` #4a505e | **7.47** | **8.08** | **6.95** | AA (old: 5.9 / 6.4 / 5.5 — passed, but see next row) |
| `--text-dim` @ 0.8 (stale cells) | **4.52** | **4.76** | 4.28 (do not stale-dim on raised) | AA on bg/panel only (old value: 3.90 on panel) |
| `--text-gated` #6b6e78 | **4.71** | **5.09** | 4.38 (never place gated on raised) | AA |
| `--accent` #2f66d0 | **4.94** | **5.34** | **4.60** | AA |
| `--ok` #177a45 | **4.97** | **5.38** | **4.63** | AA (old: 4.11 on white) |
| `--warn` #8a5208 | **5.90** | **6.38** | **5.49** | AA (old: 4.28 on bg) |
| `--high` #a2451a | **5.71** | **6.17** | **5.31** | AA (old: 4.49 on bg) |
| `--critical` #c8323a | **4.89** | **5.29** | **4.55** | AA (unchanged) |
| `--border` #d7dbe3 | 1.28 | 1.39 | 1.2 | decorative only |
| `--border-strong` #7f8797 | **3.34** | **3.61** | **3.11** | ≥3 boundary on all surfaces |
| `--on-accent` #fff on `--accent` fill | **5.34** | | | AA |
| `--on-critical` #fff on `--critical` fill | **5.29** | | | AA (black on it would be 3.97 — the reason `text-black` must go) |

**Heatmap cells** (ink over cell, both ≥ 4.5 required because the rate is
printed in the cell):

| Cell | Light ink → ratio | Dark ink → ratio |
|---|---|---|
| n1 | `--text` 13.96 | `--text` 10.44 |
| n2 | `--text` 9.87 | `--text` 7.36 |
| n3 | `--text` 5.36 | `--heat-ink` 4.75 |
| n4 | `--heat-ink` 5.34 | `--heat-ink` 9.25 |
| p1 | `--text` 14.66 | `--text` 9.39 |
| p2 | `--text` 11.23 | `--text` 5.64 |
| p3 | `--text` 6.77 | `--heat-ink` 5.11 |
| p4 | `--heat-ink` 4.67 | `--heat-ink` 10.14 |
| 0 | `--text` (= raised) | `--text` (= raised) |

### 1.4 Token changes the frontend must apply to `globals.css` (summary)

1. `--text-dim`: light `#5b6270 → #4a505e`; dark `#8b93a7 → #959db0`.
2. `--ok` light `#1f8f52 → #177a45`; `--warn` light `#a8650f → #8a5208`;
   `--high` light `#b8541f → #a2451a`.
3. `--critical` dark `#e5484d → #ef5a5e` (light unchanged).
4. Add `--border-strong`, `--text-gated`, `--on-accent`, `--on-critical`,
   `--pos`/`--neg`/`--neu`, `--heat-n4..n1`, `--heat-0`, `--heat-p1..p4`,
   `--heat-ink`, `--overlay`, `--opacity-disabled`, `--opacity-stale` — in
   **all three** blocks (`:root`, `[data-theme="dark"]`, the media query).
5. `ui.tsx`: `Button danger` hover ink `text-black` → `text-[var(--on-critical)]`;
   `Badge`, inputs/selects, chips → `border-[var(--border-strong)]`;
   `ConfirmDialog` scrim `bg-black/60` → `bg-[var(--overlay)]`; `Table`/
   `VirtualTable` gain a per-column `align: "num"` so numeric columns are
   right-aligned (§2.3).
6. Keep row separators and card edges on `--border`; do not bulk-replace.

### 1.5 Status tone usage (unchanged vocabulary, tightened)

| Tone | Token | Text / outline | Fill | Used for |
|---|---|---|---|---|
| `ok` | `--ok` | yes | never | PAPER mode, `open` network, `enabled` rule, positive sign |
| `warn` | `--warn` | yes | never | REPLAY/BACKTEST, 1–3× poll age, `stopped (maintenance margin)`, restart pending, stale-version notice |
| `high` | `--high` | yes | never | HIGH severity alerts, liquidity cap hit |
| `bad` | `--critical` | yes | only `Button danger` hover | CRITICAL severity, `STALE`, `closed` network, negative sign, errors |
| `dim` | `--text-dim` | yes | never | MARKET_DATA/SHADOW, ≤1× poll age, `disabled` rule, `unknown (venue requires API key)`, `— skipped —` |

Red/green never fill a table cell (UX §6.2). A tone is always paired with a
word or a sign; colour is reinforcement.

### 1.6 The three "can't touch this" treatments (UX §2.4)

| State | Text colour | Element opacity | Extra | Border (if a chip/button) |
|---|---|---|---|---|
| `unbuilt` | `--text-dim` | `var(--opacity-disabled)` = 0.4 | `cursor-not-allowed`, `title="Not implemented yet"` | `--border` |
| `role` | `--text-dim` | 0.4 | `cursor-not-allowed`, `title="Requires {ROLE}+"` | `--border` |
| `package` | **`--text-gated`** (solid, AA) | **1** on the label; only the leading nav/chip *icon* drops to 0.6 | 12px lock glyph (§3) + package chip `Pro` in `Badge dim`, `aria-label="Included in {Package} — upgrade"` on the glyph; whole control is a link to `upgradeHref` | `--border-strong` |

Do **not** implement the package state as `opacity-60` on the text: the
effective colour of `--text-dim` at 60 % is 2.94:1 (dark) / ~3.6:1 (light),
which fails for a clickable control. The solid `--text-gated` token is the
visually-lighter-than-disabled-but-readable value the UX spec asks for, and
the 0.6 icon opacity keeps the "lighter than the other two" read at a glance.

### 1.7 Stale rows (UX §3)

- Row age ≤ 1× poll: age cell `dim`. 1–3×: `warn`. > 3×: `bad`, literal
  text `STALE 11.2s`.
- Row dim: `opacity: var(--opacity-stale)` applied **per cell, to every cell
  except the age cell(s)** — the `STALE` carrier stays at full opacity.
  (`--critical` at 80 % over the panel is 3.89:1 dark / 3.86:1 light; the
  carrier of the state must never be the least readable thing in the row.)
- Stale dimming is valid on `--bg` and `--bg-panel` only. A hovered/selected
  stale row keeps `--bg-raised` but the frontend must not dim `--text-dim`
  cells on raised (4.4:1 / 4.28:1); simplest rule: hover suspends the dim.
- Package-gated and stale never coincide on one element; if a gated chip
  sits in a stale row, the chip keeps its own treatment.

### 1.8 Signed values

- Positive: `--pos` text with an explicit `+`. Negative: `--neg` with `-`.
  Zero / n/a / `n = 0`: `--neu` and the literal `0.00` or `—`.
- Sign is on the number, unit is on the number (`+12.4 bps`, `-0.0021 %`,
  `+142.30 USDT`) — never a bare figure that needs the colour to be read.
- Net leads, gross follows in `--text-dim` at the same size (UX §10.1); gross
  is never toned by sign, only net is — so the eye lands on the tradeable
  number.

---

## 2. Typography

Font stack: `system-ui, -apple-system, "Segoe UI", Roboto, Inter, sans-serif`
(no webfont download; console loads offline in a recording session).
Numerals: `font-feature-settings: "tnum" 1` on `body` (already), plus
`font-variant-numeric: tabular-nums slashed-zero` in every table and stat.
Identifiers (cycle IDs, session IDs, versions, `docs/…` paths):
`ui-monospace, SFMono-Regular, Menlo, Consolas, monospace` at one size step
smaller than the surrounding text.

### 2.1 Scale (px size / line-height, weight, letter-spacing)

| Style | Size/LH | Weight | Tracking | Use |
|---|---|---|---|---|
| `title` | 18 / 24 | 600 | 0 | `PageTitle` |
| `section` | 13 / 16 | 600 | +0.05em, uppercase | `Section` heading, `NavGroupHeader` (10/14 in the rail label column, as today) |
| `body` | 14 / 20 | 400 | 0 | prose, dialog body, `Stat` value (500) |
| `table` | 13 / 18 | 400 | 0 | `Table`, `VirtualTable`, drawer facts, form inputs |
| `table-dense` | 12 / 16 | 400 | 0 | optional density toggle (§2.3), event history, evidence breakdowns |
| `label` | 11 / 14 | 400 | +0.05em, uppercase | `Stat` label, chart captions (`n =`), column units |
| `badge` | 11 / 14 | 500 | +0.02em | `Badge`, age chips, package chip |
| `button` | 12 / 16 | 500 | 0 | `Button`, chips, template row |
| `mono` | 12 / 16 | 400 | 0 | IDs, paths, versions |

Minimum text size anywhere in the console is 11px; 11px is only ever
uppercase labels or badges (short, high-contrast), never a sentence.

### 2.2 Numbers in dense tables

- Numeric columns right-aligned; header of a numeric column right-aligned
  too; unit in the header (`Spread net (bps)`), not repeated per cell
  unless the unit varies per row (quote currency — then `142.30 USDT`).
- Fixed decimal places per column come from the backend string as-is; the
  frontend never rounds. If a value is wider than the column, the cell
  truncates with `title` = full string, never wraps (row height invariant).
- Sign column: the `+`/`-` is part of the string; right alignment keeps the
  sign column ragged, which is fine — tabular numerals align the digits.
- Age chips (`1.8s`, `STALE 11.2s`) are `badge` style inline after the venue
  name, not a separate column, so the table does not gain two columns.

### 2.3 Density

| Property | Standard (default) | Dense (per-user toggle, `localStorage`, table pages only) |
|---|---|---|
| Table row height | 31px (`py-1.5` + 13/18 + 1px rule) — `VirtualTable ROW_HEIGHT` | 27px (`py-1` + 12/16 + 1px) — `ROW_HEIGHT` must read the mode |
| Cell padding x | 12px | 8px |
| `Stat` cell | `p-3`, label 11 / value 14 | unchanged (strip is never dense) |
| Filter card | `p-3`, chips 24px tall | chips 22px |

Dense mode is a later nicety; nothing in the UX spec needs it on day one.
It is listed so `ROW_HEIGHT` is not hardcoded a second time.

### 2.4 Spacing

4px base. Allowed steps: 2, 4, 8, 12, 16, 24, 32, 48. Page gutter `p-4`
(< md) / `p-6` (≥ md). Section gap 24. Stats strip `gap-3` (12). Card
padding 12. Chip gap 8. Drawer padding 16. Icon-to-label gap 8.
Focus ring: 2px `--accent`, offset 2 (already in `globals.css`; accent is
≥ 4.6:1 on every surface in both themes, so it passes the 3:1 indicator
rule with margin). Border radius: 4px everywhere (`rounded`); 999px for the
notification count pill only.

---

## 3. Icons

Rules (extending `icons.tsx`'s header comment, which stays authoritative):

- Inline SVG only, `viewBox="0 0 16 16"`, `fill="none"
  stroke="currentColor" strokeLinecap="round" strokeLinejoin="round"
  aria-hidden="true"`; accessible name lives on the button/link, never on
  the `<svg>` (`aria-label` on rail buttons per UX §9).
- Two rendered sizes: **16** (nav items, inline in text, badges — today's
  15px `NavIcon` becomes 16 so it sits on the 4px grid) with
  `strokeWidth 1.3`; **20** (icon rail, bell, drawer close, empty-state
  marks) with `strokeWidth 1.5`. Stroke width is set per rendered size, not
  inherited through scaling, so 20px glyphs do not look bold.
- Geometry: 1.5px inset (`1.5…14.5`), primitives only (rect/circle/line/
  polyline/polygon/path), no traced artwork, no imported set. Filled dots
  (`fill="currentColor" stroke="none"`) mark the "active/current" element
  in a glyph, as `Scanner` already does.
- Colour: always `currentColor`; an icon never has its own colour token.
- New glyphs this spec needs (coordinates in the 16 viewBox), so the
  implementer draws exactly these and nothing is lifted:

| Name | Use | Geometry |
|---|---|---|
| `group-operate` | rail: Operate | `<rect x="2" y="2" width="12" height="12" rx="1.2" fill="currentColor" stroke="none"/>` (filled square) |
| `group-scanner-suite` | rail: Scanner Suite | `<circle cx="8" cy="8" r="6.3"/>` + `<polygon points="9,3.5 5.2,8.6 7.8,8.6 7,12.5 10.8,7.4 8.2,7.4" fill="currentColor" stroke="none"/>` (bolt in circle) |
| `group-portfolio` | rail: Portfolio | `<rect x="2" y="2" width="12" height="12" rx="1.2"/>` + `<rect x="8" y="2" width="6" height="12" fill="currentColor" stroke="none"/>` (split square) |
| `group-research` | rail: Research | circles r=1.6 at (4,4) (12,4) (8,12), lines 4,4→12,4; 4,4→8,12; 12,4→8,12 (node graph) |
| `group-control` | rail: Control | `<polygon points="8,1.8 14.2,8 8,14.2 1.8,8"/>` (diamond) |
| `group-system` | rail: System | `<polygon points="8,1.8 13.4,4.9 13.4,11.1 8,14.2 2.6,11.1 2.6,4.9"/>` (hex) |
| `chevron` | group collapse | `<polyline points="4.5,6 8,9.5 11.5,6"/>`, rotate −90° when collapsed (150ms ease) |
| `bell` | notification centre | reuse `Alerts` glyph path (already ours) |
| `lock` | package-gated | `<rect x="3.5" y="7" width="9" height="7" rx="1"/>` + `<path d="M5.5 7V5.2a2.5 2.5 0 0 1 5 0V7"/>`; rendered at **12px**, strokeWidth 1.2, inline after the label |
| `close` | drawer/dialog close | `<path d="M4 4l8 8M12 4l-8 8"/>` |
| `external` | "Open in Calculator/Funding" | `<path d="M9 2.5h4.5V7M13.5 2.5L7 9"/>` + `<path d="M6 3.5H3v9.5h9.5V10"/>` |
| `collapse-rail` | sidebar collapse toggle | `<path d="M2.5 2.5v11"/>` + `<polyline points="10,5 7,8 10,11"/>`; mirrored when collapsed |
| `check` | template saved, chip selected | `<polyline points="3,8.5 6.5,12 13,4.5"/>` |
| `plus` / `minus` | add asset, expand/collapse skip breakdown | `<path d="M8 3v10M3 8h10"/>` / `<path d="M3 8h10"/>` |
| `info` | consequence sentences, tooltips | `<circle cx="8" cy="8" r="6.3"/>` + `<path d="M8 7v4.5"/>` + dot at (8,4.8) r=0.9 |
| `warn-tri` | inline `warn` notices | `<path d="M8 2.2 14 13.5H2Z"/>` + `<path d="M8 6.5v3.5"/>` + dot at (8,11.6) r=0.8 |

- Nav item glyphs for `Evidence` (new page): `<rect x="3" y="2" width="10" height="12" rx="1"/>` + `<polyline points="5.5,9.5 7.5,7.5 9,9 11,6"/>` (a report with a small line).
- Every `NavIcon` label without a glyph falls back to the dot, as today.

---

## 4. Component specs (delta components from UX §12)

Common to all: tokens only (no hex in TSX), `text-[13px]` table style,
4px radius, focus ring from `globals.css`, keyboard contracts per UX §9.
Sizes are in px; "state" rows list visual deltas only.

### 4.1 `IconRail` + `NavGroupHeader`

**Layout (≥ md)**: rail 44px wide (`--bg-panel`, right rule `--border`),
label column 176px (total 220 ≈ today's `w-56`). Collapsed sidebar = rail
only (44px). Top bar row 40px: brand at 14/20 600 tracking +0.02em, bell,
theme toggle. Mode banner full-width under the brand row (unchanged).
Settings pinned bottom, collapse toggle beneath it (32px tall).

**Rail button** (one per group): 36×36 hit area, 20px glyph centred.

| State | Glyph colour | Background | Indicator |
|---|---|---|---|
| default | `--text-dim` | none | — |
| hover | `--text` | `--bg-raised` | — |
| group expanded | `--text` | `--bg-raised` | — |
| contains active page | `--accent` | `--bg-raised` | 2px `--accent` bar on the left edge, full button height |
| focus-visible | inherits | inherits | ring |
| collapsed-sidebar tooltip | group name in a `--bg-panel`/`--border-strong` tip, 12/16, 4px offset right, on hover **and** focus |

**`NavGroupHeader`** (label column): 24px row, `section` style at 10/14,
chevron 16px right-aligned, `aria-expanded`, `aria-controls`.
States: default `--text-dim`; hover/expanded `--text`; the group holding
the active page cannot collapse (existing behaviour kept).

**Nav item** (unchanged shape, restated): 28px row, 16px glyph + label 13/18.
default `--text-dim`; hover `--text` on `--bg-raised`; active `--text` on
`--bg-raised` + 500 weight; gated per §1.6.

Motion: expand/collapse 150ms ease-out height + chevron rotate; sidebar
collapse 200ms. Respect `prefers-reduced-motion` → 0ms.

### 4.2 `GatedControl`

Props: `state: "unbuilt" | "role" | "package"`, `reason`, `upgradeHref?`,
`as: "nav" | "chip" | "button"`. Wraps its child and applies §1.6.

| `as` | Height | unbuilt / role | package |
|---|---|---|---|
| nav | 28 | span, `title=reason`, opacity 0.4 | `<Link href={upgradeHref}>`: glyph @0.6, label `--text-gated`, lock 12px + `Badge dim` package name |
| chip | 24 | chip outline `--border`, text `--text-dim`, opacity 0.4, `aria-disabled` | outline `--border-strong`, text `--text-gated`, lock inside chip before the label, `title="Included in {Package} — Upgrade"`, click → upgrade route |
| button | 26 | existing `Button disabled` | `Button` variant: outline `--border-strong`, label `--text-gated`, lock leading, hover `--bg-raised` (it is live) |

Never `pointer-events:none` on the package state (the tooltip and click are
the point). Operator console never renders `package` (UX §2.4).

### 4.3 `RowDrawer`

- ≥ md: fixed right, width 420 (min 360, max 50vw), full height under the
  top bar, `--bg-panel`, left edge 1px `--border-strong`, shadow
  `0 0 0 1px var(--border), -8px 0 24px rgba(0,0,0,.25)` (dark) /
  `-8px 0 24px rgba(23,26,33,.12)` (light). No scrim (non-modal).
- < md: full-screen, `--overlay` scrim behind, slides up.
- Header 44px: 13/18 600 title (`BTC/USDT Binance → OKX`), age badge,
  close button (20px `close`, 32×32 hit). `role="complementary"`,
  `aria-label="Detail: …"`, focus to close on open, return to row on close.
- Body: `Section` blocks (`BUY SIDE`, `SELL SIDE`, `CALCULATOR`), fact rows
  as a 2-column definition list (`label` style left, `table` style right,
  numbers right-aligned), 8px row gap.
- Footer 48px: primary link `Open in Calculator ↗` (`--accent`, `external`
  glyph), secondary `Close`.
- States: `loading` — the header renders from the row immediately, body
  shows `Loading detail…`; `error` — `ErrorBox` inside the body, header
  stays; `stale` — the age badge in the header carries `STALE {age}` and
  the body facts are **not** dimmed (a drawer is a deliberate look, the
  badge is enough).
- Motion 200ms translate-x; reduced-motion → none.

### 4.4 `NotificationBell` + `NotificationPanel`

**Bell**: 32×32 button, 20px glyph, `--text-dim` → `--text` on hover;
count pill top-right: min-width 16, height 16, 11/14 600, `--critical` fill
with `--on-critical` ink when any unseen CRITICAL, else `--accent` fill with
`--on-accent`; hidden at 0. `aria-label="Notifications, {n} unseen"`.

**Panel**: anchored under the bell, width 360 (< md: full width below the
top bar), `--bg-panel`, `--border-strong` edge, same shadow as the drawer,
`aria-live="polite"`. Header 36px: `NOTIFICATIONS` section style + `Mark
all seen` text button. Items 56px each, up to 8 (CRITICAL pinning per UX
§2.5):

```
[CRITICAL] [ALERT]  Spread collector lost Binance feed        2m
                    → Alerts
```

- Row: severity `Badge` (`severityTone`), origin `Badge dim` (`ALERT`/`RULE`),
  summary 13/18 single line truncated, relative time `label` style right,
  second line: source page link `--accent` 12/16.
- Unseen: 2px `--accent` bar at the left edge + 500 weight summary. Seen:
  no bar, 400 weight. Hover: `--bg-raised`.
- Footer 36px: `+N more — view all` (when overflowing) and always
  `View all in Alerts →`.
- Empty: `No unresolved alerts or rule events.` in `--text-dim`.
- Escape closes, focus returns to the bell; click-outside closes.

### 4.5 `RuleEvidenceTable`

Built on `Table` with numeric alignment. Columns and treatments:

| Column | Style |
|---|---|
| Rule / Strategy | `--text`, link to the rule (operator) / plain (client) |
| Alerts fired, Executed | numeric, `--text` |
| Skipped (top reason) | `12 (liquidity)`, `--text-dim`; the cell is a disclosure button (`plus`/`minus` glyph 12px) — expanded state inserts one full-width sub-row (`--bg-raised`, 12/16) `liquidity: 4 · data age: 2 · no paper balance: 1`, keyboard-reachable |
| Net PnL | numeric, signed per §1.8, **500 weight** (the lead number) |
| Hit rate | numeric, `--text`; when `n < 30` the cell appends a `Badge dim` `thin` — the number is never hidden, only annotated |
| Mean lifetime | numeric `--text` |
| Sample size (n) | numeric, always present, `--text`; `n = 0` rows render every other numeric cell as `0` / `—` in `--neu` |

Header row shows units. Footer line (12/16 `--text-dim`): `as of {generated_at}` and, on Evidence, the `docs/campaigns/…` citation in `mono`, linked.
Empty: the two sentences from UX §6.7 verbatim.

### 4.6 `FundingHistoryChart`

Hand-rolled SVG like `PnLSeriesChart`; backend `{at, rate}` only.

- Size: width = container (max 640), height 120; one chart per venue,
  stacked with 16px gap; venue name as a 13/18 600 caption above, `n = {k}`
  and `range {min} … {max}` as `label` captions below.
- Axes: zero line `--border-strong` 1px (the sign boundary must be visible
  at 3:1); x tick labels every ~6 points, 11/14 `--text-dim`, UTC `HH:MM`
  (or `DD HH:MM` for > 72h); y labels min/0/max only, right-aligned in a
  40px left gutter, verbatim backend strings.
- Series: a single 1.5px line in `--accent`; area between line and zero
  filled `--pos` at 0.18 where rate > 0 and `--neg` at 0.18 where rate < 0
  (sign is also shown as text in the hover and table, so fill is
  reinforcement). Points ≥ 8px apart get a 2px dot; below that, none.
- Hover/focus: vertical `--border-strong` guide + tooltip (`--bg-panel`,
  `--border-strong`) `2026-08-26 08:00Z · +0.0100 % (8h)`; keyboard: the
  SVG is focusable, ←/→ move the guide.
- Heatmap variant (Funding page overview, venues × time buckets): cells
  16×16 with 1px `--bg` gap, colour from §1.2 ramp, breakpoints supplied by
  the backend's bucket edges (never computed in the client); the rate is
  printed in the cell at 11/14 with the ink per §1.3 when cells are ≥ 40px
  wide, otherwise only in the hover/tooltip and the table beneath.
- `aria-label` per UX §9; the data table beneath remains the accessible
  source.
- Empty: the two Funding empty sentences (UX §6.4) verbatim, no axes drawn.

### 4.7 `FilterCard`

`--bg-panel`, `--border`, `p-3`, `rounded`, collapsible below md (`Filters
(n active)` toggle button with a count `Badge`).

Rows (wrap at narrow widths, 8px gaps):

1. **Venue chips** — group label (`label` style) then chips. Chip: 24px
   tall, 12/16 500, `px-2`, outline `--border-strong`.
   - off: text `--text-dim`, no fill
   - on: fill `--accent`, text `--on-accent`, `check` glyph 12px leading
   - hover (off): `--bg-raised`; hover (on): unchanged
   - unavailable venue: `unbuilt` treatment + `title` = verbatim backend reason
   - package-gated: §4.2 chip
2. **Numeric filters** — labelled inputs 26px tall, `table` style, right-
   aligned digits, unit suffix inside the field (`bps`, `USDT`, `s`, `% APR`)
   in `--text-dim`; border `--border-strong`, focus border `--accent`;
   invalid: border `--critical` + 12/16 message in `--critical` beneath
   (client-side guard only; backend rejection replaces the message).
3. **Quote select + base allow/deny** — tokenised input; tokens are chips
   with a 12px `close` glyph, Enter adds, Backspace on empty removes last.
4. **Template row** — `select` (saved templates), `Save as template…`
   (reveals an inline 160px name field + `Save`/`Cancel`), `Reset filters`
   as a text button in `--text-dim`. Applied-template name shows as a
   `Badge dim` next to the select; a modified-since-load template shows
   `edited` in `warn`.

Active-filter count = every control differing from the page default.

---

## 5. Chart palettes

### 5.1 Signed series (PnL, spread, carry)

| Role | Token | Dark | Light | Encoding besides colour |
|---|---|---|---|---|
| cumulative / primary line | `--accent` | #4f8cff | #2f66d0 | solid 1.5px |
| positive region / bar | `--pos` | #2fbf71 | #177a45 | solid; `+` in labels |
| negative region / bar | `--neg` | #ef5a5e | #c8323a | fill at 0.18 + **dashed** 1px outline `4 3` — the dash is the deuteranopia-safe distinction from positive |
| drawdown area | `--neg` @0.18 | | | as today |
| zero / neutral | `--neu`, zero line `--border-strong` | | | |
| second series (compare) | `--warn` | #e6a23c | #8a5208 | dotted `1 3` |
| histogram bars | `--accent` @0.75 | | | as today |

Green/red keep the trading convention because every value also carries
sign text; for a red-green-confused reader the dashed outline and the sign
are the carriers. Do not add a third hue for "break-even".

### 5.2 Funding heatmap (diverging, colour-blind safe)

Blue (negative rate — shorts pay) ↔ neutral (`--bg-raised`) ↔ orange
(positive rate — longs pay). Blue/orange is distinguishable under
deuteranopia, protanopia and tritanopia; ramp values in §1.2, ink rule in
§1.3. Legend always printed under the heatmap with the backend's bucket
edges (`≤ -0.03 %`, `-0.03…-0.01 %`, …) and the words `shorts pay` /
`longs pay` at the ends. Never more than 4 steps per side; a `n/a` cell is
`--bg` with a `—`.

### 5.3 Categorical (venues on one chart, at most 5)

Order: `--accent`, `--warn`, `--ok`, `--high`, `--text-dim`, with line
styles solid / dashed / dotted / dash-dot / solid-thin in the same order, and
the venue name printed at the line end. Beyond five venues, split into
stacked per-venue charts (which is the Funding page's default anyway).

---

## 6. Marketing site style guide (shares the console tokens)

Purpose: the signals-product marketing pages and the client-console login/
upgrade pages look like the product they sell, without pretending to be a
trading terminal.

- **Tokens**: the same `globals.css` file; light is the default, a dark
  hero/section opts in with `data-theme="dark"` on that section only. No
  marketing-only colours; the only additive is an `--accent` at 10 %
  (`color-mix(in srgb, var(--accent) 10%, transparent)`) for soft
  highlight bands.
- **Type scale** (marketing only; the console never uses these):
  `display` 40/48 700 (−0.01em), `h2` 28/36 600, `h3` 20/28 600, `lead`
  18/28 400, `body` 16/26 400, `small` 14/20, `caption` 12/16 `--text-dim`.
  Measure max 68ch. Same system font stack; same tabular numerals wherever
  a number appears.
- **Layout**: 12-column, 1120px max content width, 24px gutters (16 below
  md), vertical rhythm on 8px, sections 64/96px apart. Radius 4 (cards) and
  6 (screenshots). Shadows only on product screenshots.
- **Buttons**: primary = `--accent` fill / `--on-accent` ink, 40px tall,
  14/20 500, `px-4`; secondary = outline `--border-strong`, `--text`;
  danger never appears on marketing pages. Focus ring as console.
- **Product imagery**: real console screenshots from the paper/record
  modes, mode banner visible ("PAPER TRADING ONLY — live execution
  permanently disabled" is not cropped out), no mock data that looks like
  live returns. Charts in screenshots use the palettes above.
- **Numbers**: any figure (hit rate, sample size, net paper PnL, venue
  count) is rendered with its `n` and a caption `Source: docs/campaigns/…`
  linking the report; no figure appears without one. Percentages never
  stand alone as a headline.
- **Copy rules** (from UX §10, restated for marketing): net-of-fees before
  gross; sample size in the same sentence as any rate; never "guaranteed",
  "risk-free", "certain", "always", "proven"; "we never touch your funds /
  never ask for an exchange key" is the credential line; performance
  sentences reuse the client Evidence footer verbatim: *"Simulated on
  paper. Past paper performance is not a projection or guarantee of
  future results."*
- **Icons**: the console glyph set at 20/24px (24px = viewBox 16 rendered
  at 24, stroke 1.7); no third-party icon set on the marketing site either.
- **Package cards** (Starter/Trader/Pro/Desk/Enterprise): identical card
  shape, the recommended tier gets an `--accent` top rule (3px), limits are
  rows with the `check` glyph or a `—` in `--text-dim`; the limit numbers
  come from the product manager's package table (`docs/design/packages.md`),
  never typed into the page.
- **Status vocabulary** on the site's own status/roadmap page: the console
  `Badge` tones and words (`ok`/`warn`/`bad`/`dim`), not a separate scheme.

---

## 7. Checklist for the implementer

1. Apply §1.4 to `globals.css` (all three blocks) and `ui.tsx`.
2. Add the contrast test (§0) with the **bold** ratios in §1.3 as fixtures
   (tolerance ±0.05).
3. `NavIcon` 15 → 16px; new glyphs from §3 added to `icons.tsx` under the
   same header comment.
4. Build the seven components to §4; `GatedControl` must use
   `--text-gated`, not opacity, for the package label.
5. Stale dimming per cell excluding the age cell (§1.7).
6. Charts read only CSS variables; the heatmap ink switch follows §1.3.
7. Marketing site imports the same `globals.css`; no second token file.
