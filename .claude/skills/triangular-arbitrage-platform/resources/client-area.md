# Resource: Web Client Area

Authoritative documents: `docs/architecture.md` §8–§9 (realtime + client
architecture); SKILL.md §30–§55 (page-by-page requirements).
Implementation home: `web/`.

Working rules:
- Next.js App Router + TypeScript + Tailwind; dark-mode-first; desktop-
  first responsive; information-dense but hierarchical.
- Navigation per SKILL.md §31: Overview, Scanner, Triangles,
  Opportunities, Paper Trading, Orders, Fills, Portfolio, Balances,
  PnL & Analytics, Exchanges, Markets, Strategies, AI Advisor, Risk
  Center, Replay & Backtesting, Reports, Alerts, Telegram, System Health,
  Audit Log, Users & Security, Settings.
- Typed API client aligned with the OpenAPI contract; all financial values
  are strings from the backend — the client formats (lib/format), never
  computes profitability/fees/risk.
- Real-time via the WS topic protocol (subscribe → snapshot → seq'd
  diffs; gap ⇒ resubscribe; server resync flag handled). No 1s polling.
- Scanner/orders/fills tables: virtualized rows, keyed row updates,
  batched store updates; page-wide rerenders per tick are defects.
- Every page ships loading/empty/error/degraded states; degraded banner
  driven by the health topic (stale data must be visibly stale).
- Mode banner (MARKET_DATA/RECORD/REPLAY/BACKTEST/PAPER/SHADOW) always
  visible; PAPER is clearly labeled everywhere P&L appears.
- Dangerous actions (paper reset, config change, AI approval) use
  explicit confirmation with before/after diffs; RBAC hides what the role
  cannot do, backend remains the gate.
- Accessibility: keyboard navigable tables/forms; status conveyed by
  text/icon, not color alone.
