---
description: Resume the audit remediation on the working branch from docs/audit/handover-status.md
agent: build
---
Continue the audit remediation of this repository on branch
`claude/triangular-arbitrage-platform-969nkv` (pull request #20, draft, base `master`).

Focus for this run (empty means "next item in the handover order"): $ARGUMENTS

State right now:
!`git status --short | head -20`
!`git log --oneline -10`

Read, in this order: @AGENTS.md, @docs/audit/handover-status.md,
@docs/audit/implementation-roadmap.md (the P0/P1/P2 status tables), then the
sections of @docs/audit/master-report.md that the item you pick refers to.

Work the remaining items in the handover order unless the focus above says
otherwise: the console findings F5 and F6 from docs/audit/ui-ux-audit.md,
the breaker acknowledgement endpoint (`POST /api/v1/risk/breakers/close`,
ADMIN, CSRF, audited, type-to-confirm, calling `risk.Registry.Close`, with a
Risk Center control), the remaining P2/P3 roadmap items (venue fee rates,
topology and rules refresh, the CreateUser organisation-1 follow-up), then the
full regression (`/regression`) and the final review in
docs/audit/master-report.md — ratings per area, problems fixed, remaining
risks, tests added, measured before/after, and the verdict "NOT READY FOR LIVE
TRADING" or "READY FOR CONTROLLED LIVE VALIDATION"; never "guaranteed
profitable". Update the pull request description to match.

Rules: one coherent change per commit with tests, the commit conventions and
validation steps in AGENTS.md, no force pushes or history rewrites, nothing
that touches live trading or live balances. Be skeptical of apparent
profitability; understand, prove, then improve.
