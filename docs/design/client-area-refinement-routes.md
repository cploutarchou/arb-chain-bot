# Client-area refinement — complete old→new route map (T-087)

<!-- GENERATED FILE — do not edit by hand.
     Regenerate with: cd web && node scripts/gen-route-map.mjs
     Source of truth: web/src/lib/nav.ts and the pages on disk. -->

Generated from `web/src/lib/nav.ts` and a filesystem scan of
`web/src/app`, so this table cannot drift from the navigation the app
ships. The generator exits non-zero if any page resolves to no
navigation entry, which is what makes "every route is preserved" a
checked claim rather than an assertion.

## Counts

| | Before | After |
| --- | --- | --- |
| Primary navigation choices | 6 groups, all expanded | 7 destinations |
| Standing links rendered at once | 29 + 3 pinned = 32 | 7 primary (secondary appears in context) |
| Duplicate group control (icon rail) | yes | removed |
| Links visible at once (worst case) | 32 | 20 = 7 primary + 13 secondary |
| Navigation entries defined | 32 | 38 standing + 3 contextual |
| Pages served | 37 | 37 (unchanged) |
| Pages highlighting no nav entry | 3 | 0 |

Secondary entries per destination:

| Destination | Secondary entries |
| --- | --- |
| Overview | 1 |
| Discover | 7 |
| Paper Trading | 5 |
| Research & Results | 6 |
| Alerts & Rules | 4 |
| Settings | 2 |
| Operations | 13 |

## Every route

| Route | Was | Primary destination | Presented as | Activations from Overview |
| --- | --- | --- | --- | --- |
| `/` | (redirect shim) | — | outside the console shell | — |
| `/ai` | Research › AI Advisor | Research & Results | Research & Results › AI advisor | 2 |
| `/alerts` | Control › Alerts | Alerts & Rules | Alerts & Rules › Platform alerts | 1 |
| `/audit` | System › Audit Log | Operations | Operations › Audit log | 2 |
| `/auto-paper` | Scanner Suite › Auto-Paper | Paper Trading | Paper Trading › Rule simulations | 2 |
| `/billing` | footer › Billing | Settings | Settings › Billing | 2 |
| `/calculator` | Scanner Suite › Calculator | Discover | Discover › Spreads calculator | 2 |
| `/campaigns` | Research › Campaigns | Research & Results | Research & Results › Campaigns | 2 |
| `/cycles/[id]` | (no nav entry — detail page, highlighted nothing) | Paper Trading | contextual — opened from Paper Trading | in context |
| `/exchanges` | System › Exchanges | Operations | Operations › Venues | 2 |
| `/fills` | Portfolio › Fills | Paper Trading | Paper Trading › Fills | 2 |
| `/funding` | Scanner Suite › Funding | Discover | Discover › Funding | 2 |
| `/login` | (outside the shell) | — | outside the console shell | — |
| `/onboarding` | (no nav entry — highlighted nothing) | Overview | contextual — opened from Overview | in context |
| `/opportunities` | Operate › Opportunities | Discover | Discover › Opportunities | 2 |
| `/opportunities/[id]` | (no nav entry — detail page) | Discover | Discover › Opportunities | 2 |
| `/orders` | Portfolio › Orders | Paper Trading | Paper Trading › Orders | 2 |
| `/org` | footer › Organisation | Settings | Settings › Organisation | 2 |
| `/overview` | Operate › Overview | Overview | Overview › Overview | 0 (landing page) |
| `/paper` | Operate › Paper Trading | Paper Trading | Paper Trading › Triangular simulations | 1 |
| `/perpetuals` | Scanner Suite › Perpetuals | Discover | Discover › Perpetuals | 2 |
| `/pnl` | Portfolio › PnL & Analytics | Research & Results | Research & Results › Results & analytics | 1 |
| `/portfolio` | Portfolio › Portfolio & Balances | Paper Trading | Paper Trading › Balances | 2 |
| `/replay` | Research › Replay & Backtesting | Research & Results | Research & Results › Replay & backtesting | 2 |
| `/reports` | Control › Reports | Research & Results | Research & Results › Engine reports | 2 |
| `/risk` | Control › Risk Center | Operations | Operations › Risk centre | 1 |
| `/scanner` | Operate › Scanner | Discover | Discover › Triangular scanner | 2 |
| `/scanner-alerts` | Scanner Suite › Alert Rules | Alerts & Rules | Alerts & Rules › Alert rules | 2 |
| `/screener` | Scanner Suite › Screener | Discover | Discover › Spot screener | 1 |
| `/screener-reports` | Scanner Suite › Evidence — Screener Reports | Research & Results | Research & Results › Screener evidence | 2 |
| `/screener-reports/[id]` | (no nav entry — detail page, highlighted nothing) | Research & Results | Research & Results › Screener evidence | 2 |
| `/settings` | footer › Settings | Settings | Settings › Settings | 1 |
| `/strategies` | Control › Strategies | Alerts & Rules | Alerts & Rules › Strategies | 2 |
| `/system` | System › System Health | Operations | Operations › System health | 2 |
| `/telegram` | System › Telegram | Alerts & Rules | Alerts & Rules › Telegram | 2 |
| `/triangles` | Operate › Triangles | Discover | Discover › Triangles | 2 |
| `/triangles/[id]` | (no nav entry — detail page) | Discover | Discover › Triangles | 2 |

## Settings categories and preserved anchors

Every anchor that existed before the refinement is marked
**legacy** and must still activate and focus its category; the e2e
suite asserts each one.

| Anchor | Section | Category | Pre-existing | Platform staff only |
| --- | --- | --- | --- | --- |
| `#account` | Account & session | account | new | no |
| `#notifications` | Notifications | notifications | yes | no |
| `#operating-mode` | Operating mode | administration | yes | yes |
| `#markets` | Markets & assets | administration | yes | yes |
| `#venues` | Venues & fees | administration | new | yes |
| `#scanner-suite` | Scanner Suite | administration | yes | no |
| `#ai` | AI settings | administration | yes | yes |
| `#logging` | Logging & access | administration | yes | yes |
| `#users` | Users & roles | administration | yes | yes |
| `#security` | Vault & security | administration | yes | yes |
| `#platform-versions` | Settings history | administration | yes | yes |
| `#strategy-risk` | Strategy & risk | administration | new | no |
