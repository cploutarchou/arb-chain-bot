# Client-area refinement — measured baseline (T-087)

Recorded 2026-09-12 by the full-stack lead before any implementation, so
the before/after report rests on measurements rather than impressions.
Evidence for the visual claims is the desktop Brave session captured in
`client-area-audit-2026-09-12/` (seven numbered screenshots + README).

Everything below was read out of the code in this worktree at `8a6ff63`.

## 1. Route inventory — 37 pages

Counted with `find web/src/app -name page.tsx`. Every one of these must
stay reachable at its existing URL with its existing authorization.

| # | Route | Page file |
| --- | --- | --- |
| 1 | `/` | `app/page.tsx` (redirect shim) |
| 2 | `/login` | `app/login/page.tsx` |
| 3 | `/overview` | `app/overview/page.tsx` |
| 4 | `/scanner` | triangular scanner |
| 5 | `/triangles` | `app/triangles/page.tsx` |
| 6 | `/triangles/[id]` | triangle detail |
| 7 | `/opportunities` | `app/opportunities/page.tsx` |
| 8 | `/opportunities/[id]` | opportunity detail |
| 9 | `/paper` | triangular paper simulation |
| 10 | `/cycles/[id]` | paper cycle detail |
| 11 | `/portfolio` | portfolio & balances |
| 12 | `/pnl` | PnL & analytics |
| 13 | `/orders` | orders |
| 14 | `/fills` | fills |
| 15 | `/campaigns` | campaigns |
| 16 | `/replay` | replay & backtesting |
| 17 | `/ai` | AI advisor |
| 18 | `/strategies` | strategies |
| 19 | `/risk` | risk centre |
| 20 | `/alerts` | incident alerts |
| 21 | `/reports` | engine operations reports |
| 22 | `/exchanges` | venue capabilities |
| 23 | `/system` | system health |
| 24 | `/audit` | audit log |
| 25 | `/telegram` | Telegram control surface |
| 26 | `/screener` | cross-exchange spot screener |
| 27 | `/perpetuals` | perpetuals basis |
| 28 | `/funding` | funding monitor |
| 29 | `/calculator` | spreads calculator |
| 30 | `/scanner-alerts` | Scanner Suite alert rules |
| 31 | `/screener-reports` | screener paper evidence |
| 32 | `/screener-reports/[id]` | screener evidence detail |
| 33 | `/auto-paper` | rule-based paper execution |
| 34 | `/settings` | settings (13 sections, 9 anchors) |
| 35 | `/org` | organisation |
| 36 | `/billing` | billing |
| 37 | `/onboarding` | setup wizard |

## 2. Current navigation — 29 group links + 3 pinned + a duplicate rail

`ConsoleShell.tsx`'s `GROUPS` constant, counted: Operate 5, Portfolio 4,
Research 3, Control 4, System 6, Scanner Suite 7 = **29 links across six
groups**, all expanded by default, in an independently scrolling label
column. Organisation, Billing and Settings are pinned below it (3 more).
`IconRail` renders a **second, simultaneous** control for the same six
groups beside the label column — two affordances for one choice.

Two structural defects in how the shell tracks the current page:

1. **Selection keys off display text.** `ConsoleShell` takes
   `active: string` and matches it against `NavItem.label` by string
   equality (`g.items.some((i) => i.label === active)`), and `NavIcon`
   looks up its glyph by the same label. Renaming any label silently
   breaks both the active state and the icon. The code says so itself:
   the Scanner Suite comment warns the label "must not collide" because
   "both GLYPHS and NavContent's `activeGroupTitle` lookup key off this
   exact string".
2. **Three pages already highlight nothing**, because their `active`
   value matches no label:
   - `cycles/[id]/page.tsx` passes `active="Paper"` (label is `Paper Trading`)
   - `screener-reports/*` pass `active="Screener Reports"` (label is
     `Evidence — Screener Reports`)
   - `onboarding/page.tsx` passes `active="Onboarding"` (no such label)

   This is a pre-existing bug, not something the refinement introduces,
   and it is the direct argument for stable IDs plus route matching.

## 3. Settings anchors — the real list is nine

Grepped from `app/settings/page.tsx`, where all nine are wrapper `<div
id=…>` elements around a flat sequence of sections (lines 867–894):

`#operating-mode`, `#markets`, `#scanner-suite`, `#logging`, `#ai`,
`#platform-versions`, `#users`, `#notifications`, `#security`

This matches the nine the command lists. Three sections carry **no**
anchor today and so cannot be deep-linked: Setup wizard, Session,
Venues, Strategy & risk, Security posture. Anchors currently linked from
elsewhere in the console: `settings#markets`, `settings#users`,
`settings#notifications`, `settings#platform-versions`.

The page is one continuous form of 13 sections in a single client
component (918 lines) plus `PlatformSections.tsx` (2302 lines) — the
"continuous mixed-purpose settings form" the audit describes.

## 4. Shared seams in play

| File | Lines | Role |
| --- | --- | --- |
| `components/ConsoleShell.tsx` | 872 | shell, nav, mode banner, restart banner |
| `components/PlatformSections.tsx` | 2302 | platform settings sections |
| `components/ui.tsx` | 816 | primitives (Table, VirtualTable, Stat, Badge, …) |
| `components/icons.tsx` | 513 | `NavIcon`, keyed by display label |
| `lib/decimal.ts` | 154 → 473 | exact-decimal helpers (+ presentation, this task) |
| `lib/format.ts` | 56 | ages, bytes, durations |
| `lib/auth.tsx` | 254 | session, `can()`, entitlements |

`lib/format.ts` and `lib/decimal.ts` had **no bounded-precision
presentation helper at all** before this task: `decimal.ts` covered
shifting, comparison, sign and subtraction; `format.ts` covered ages,
bytes and durations. That absence is the direct cause of the
twenty-plus-fractional-digit cells in audit states 2, 3 and 4 and the
truncated drawdown in state 1 — each page printed the backend's exact
string straight into a fixed-width cell.

## 5. Test baseline

- `web/e2e/console.spec.ts` — 1755 lines, the only frontend test file.
- No unit-test runner existed in `web/`: `package.json` had `dev`,
  `build`, `start`, `lint` (eslint + `scripts/check-contrast.mjs`),
  `typecheck`, `check:contrast`. No vitest, no jest.
- `npm run lint` also runs 54 contrast assertions over the tokens.
- `scripts/e2e.sh` builds `arbd` and runs it in PAPER mode on
  **127.0.0.1:18080** with Playwright's own Next dev server on
  **127.0.0.1:3100** — a dedicated disposable backend, separate from the
  operator's research session.

Two e2e assertions the navigation change necessarily touches:

| Location | Assertion | Treatment |
| --- | --- | --- |
| `console.spec.ts` ~1144 | "Scanner Suite" group text is visible and each of its 7 labels is an `exact: true` link | rewrite for discoverability through the new structure |
| `console.spec.ts` ~868–879 | a VIEWER sees **zero** `Audit Log` links and a `[title="Requires OPERATOR or ADMIN"]` gated entry | **preserved, not weakened** — the test navigates to wherever the entry now lives and still asserts both halves |

## 6. Verification capability in this environment

| Capability | State |
| --- | --- |
| `npm ci`, lint, typecheck, build | available (deps installed in this worktree) |
| Unit tests | added this task: `npm run test:unit`, Playwright runner, no browser, no backend |
| `scripts/e2e.sh` | available; dedicated backend on :18080, Next on :3100 |
| Go toolchain checks | to be run and recorded even though no Go is expected to change |
| Browser | A connected Chrome browser (one local Linux instance) — used read-only |
| GitHub Actions CI | **unverifiable**: `docs/PENDING.md` §0 records repo-wide Actions failure since 2026-08-31 with no runner assigned. CI-equivalent means the local suite here. |

### Runtime isolation rules adopted

- The operator's research session is live: `arbd` on **:8080** and a
  `next-server` on **:3000** serving the **main checkout's** `.next`.
  Neither process is restarted, killed or written to, and no mutating
  request is issued to :8080. :3000 can never display this worktree's
  changes, so every "after" capture comes from a dev server started in
  this worktree against the disposable backend.
- This worktree is a separate directory, so its `web/.next` is not
  shared with the running :3000 server. Within the worktree, `next
  build` and `next dev` **do** share `web/.next`, so they are strictly
  sequenced, never concurrent (the audit's `846.js` chunk failure).
- `ARB_BACKEND_URL` stays explicit everywhere; no default proxy
  destination is introduced (T-098).

## 7. Before/after evidence plan

The seven audit PNGs **are** the "before" and are not recaptured: they
are native-viewport captures of live changing data, and the README says
values differ between captures. "After" captures reproduce the same seven
states from this worktree's dev server, plus the responsive/theme/zoom
matrix (1440×900, 1024×768, 768×1024, 390×844; light and dark; 200%).
