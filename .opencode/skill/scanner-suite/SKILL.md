---
name: scanner-suite
description: 'Workflow and non-negotiables for the Scanner Suite — cross-venue spot screener, perpetuals basis/funding monitor, spreads calculator, alert rules to Telegram, and automatic PAPER execution over Binance, OKX, Bybit, Bitget, Gate, MEXC public data. Use for any screener/perpetuals/funding/alert/auto-paper work (internal/screener, screener API, Scanner Suite console pages). Use when the request mentions: "screener", "cross-exchange spread", "perpetuals", "funding rate", "basis", "carry", "spreads calculator", "alert rule", "auto-paper", "auto execute", "arbitragescanner".'
---
# Scanner Suite workflow

Task: the task the user named (a slash command passes it as its arguments).
Design of record: docs/design/scanner-suite.md (§0 is the operator's
request as a command; §6 the acceptance per task). Plan: MASTER_PLAN
Phase 22 (T-065..T-072). Endpoint facts: docs/research/screener-endpoints.md
— use ONLY fields marked VERIFIED; anything UNVERIFIED is re-verified
against the venue's current official docs (URL + access date) before use.

## Non-negotiables

- Public market data only. Collectors never sign requests; the vault's
  exchange credential group is never resolved (`secrets.IsConsumable`
  returns false for it and stays that way). Where a venue gates
  deposit/withdraw status behind a key, the UI shows "unknown (venue
  requires API key)" — never inferred from another source.
- Automatic execution is PAPER only, through the existing paper ledger
  (orders/fills/cycles tagged with strategy and venue). `LiveExecutor`
  stays `ErrLiveTradingDisabled`; no code path places a real order.
- Money math is decimal (`internal/exchange/decimal.go` types); never
  float64 in a price, size, fee or PnL path. Never edit internal/risk,
  internal/pricing, internal/simulation arithmetic as a side effect.
- Every displayed number is net of fees and carries a data age; nothing
  is described as guaranteed, risk-free or as a track record.
- Venue rate limits are enforced by construction (per-venue gate like
  `binance.restGate`); a 429/418 pauses that venue for Retry-After.

## Steps

1. Read the design §2 for the package seam you are touching; keep the
   normalised `Quote`/`Perp` types as the only contract between
   collectors and math.
2. Backend first: settings document (validated, versioned, `field_timing`),
   read models, mutations with RBAC (`screener:view` OPERATOR+,
   `screener:config` ADMIN), CSRF, audit, `parent_version`.
3. Tests: fixture-based collector tests (recorded JSON responses per
   venue under testdata/), golden tests for spread/basis/carry math,
   API tests for RBAC/CSRF, e2e for pages.
4. Console: Scanner Suite group; stats strip → filter card (templates)
   → dense virtualised table → row expand; auto-refresh at the poll
   interval; light + dark theme tokens. Own components and copy only.
5. Before DONE: `/review` on the diff, `go test ./...`, `tsc`,
   eslint, e2e; MASTER_PLAN status updated honestly with numbers.

## Definition of "profitable" for this suite

A rule is reported with: alerts raised, executed on paper, skipped (by
reason), net PnL after fees, hit rate, mean lifetime, inventory drift
between venues. Those numbers are the output; no adjective replaces them.
