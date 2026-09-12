# Pending work

Generated 2026-08-31 from `docs/MASTER_PLAN.md` at `1f78cfc`. Every entry
below is a task that is **not** DONE in the plan. Tasks that are DONE,
FIXED or CLOSED are omitted — see MASTER_PLAN for the full history.

Status vocabulary is the plan's own: TODO (not started), IN_PROGRESS,
PARTIAL (some of it shipped), BLOCKED (something must happen first),
OPEN (a decision is required).

---

## 0. Blocking everything else

### CI is failing repo-wide (not a task — an account issue) — RESOLVED 2026-09-12

Since 2026-08-31 ~19:41 UTC every GitHub Actions run fails 2–6 seconds
after being created, on **every** branch including `master`, with
`runner_id: 0`, no runner assigned and no logs. Nothing executes. The
workflow file is unchanged and the same commits passed earlier the same
week. This is almost certainly exhausted Actions minutes or a spending
limit, and it needs fixing before any code change can be trusted to land
green. Runs `33431928292` (branch, two attempts) and `33432830563`
(master) are the evidence.

**Resolved 2026-09-12** — runs execute and pass again with no workflow
change on our side, consistent with a monthly minutes/spending reset:
three consecutive green `master` runs, `34706643463` → `34708592768` →
`34719294647` (pushed 2026-09-12 21:12 UTC, all five jobs green). This
section stays as the record of the outage window (2026-08-31 →
2026-09-12), not as a live blocker.

---

## 1. Waiting on an operator decision

| Task | What it is | What is needed |
| --- | --- | --- |
| **T-106** | Carry lane viability — keep, restrict or retire | Pick one of three options. Recommendation on file: keep it at a 48 h horizon and treat zero signals as the correct output. Consequence to accept: carry then contributes ~nothing to the §8 evidence floors, so cross-venue spot becomes the lane that reaches the production gate. |

T-097's open half folded into this: the entry universe is now gated on
settled-funding confirmation, and what remains is the strategy question
T-106 asks.

---

## 2. Blocked

| Task | What it is | Blocker |
| --- | --- | --- |
| **T-046** | Profitability validation campaign | IN_PROGRESS with one sample. Acceptance needs a real multi-regime run. **This is the gate on T-050/T-051** — the shortest path to live venue breadth runs through here, not through the connectors. |
| **T-050** | Second exchange: OKX connector | Gate 1 of the first-exchange definition of done: the T-046 campaign verdict. Research (T-047) is cleared. Reviewed 2026-08-29 and confirmed **not stale** — the gate exists to stop a second live connector being built before the first exchange has a verdict. |
| **T-051** | Bybit, Bitget, Gate connectors | Behind T-050, plus fresh per-venue research. |
| **T-095** | Production execution gate | Blocked by design. Needs ≥30 days positive auto-paper evidence across regimes, a security review, an operator legal decision under `docs/decisions/`, and a human-reviewed code change replacing `ErrLiveTradingDisabled`. No work starts before the first three exist. |

---

## 3. In progress / partially shipped

| Task | What it is | What remains |
| --- | --- | --- |
| **T-075** | Venue breadth (screener) | Tier-1/2/3 shipped and enabled by default (15 venues); the two recorded soak regressions are fixed (HTX rate refusals classified + Coinbase partial-poll/burst adaptation, `768eda1`/`137698d`). Endpoint research for the remainder landed 2026-09-13 under `docs/research/venues/{upbit,bithumb,lbank,phemex}.md`. **Bithumb and Phemex collectors shipped 2026-09-13** (Tier-4, opt-in until a first live soak; Bithumb: KRW+BTC bulk books, spot-only; Phemex: bulk spot+USDT-M perp tickers, scaled-integer decoding, venue-verified fees). **Remaining: the Upbit and LBank collectors, and the Tier-4 soak + default-enable decision.** |
| **T-078** | Nightly paper report | Code shipped 2026-08-27. Filing a real ≥30-day window under `docs/campaigns/` has not happened — no run that long exists yet. |
| **T-079** | Operations automation | Self-healing collectors done. Scheduled migrations, backups, health checks and alerting on failure: not started. |
| **T-083** | Paddle billing | Implemented; a sandbox run against the real Paddle account and catalogue is still owed. |
| **T-084** | Affiliate programme | Accrual ledger, maturation job, payouts report and payout recording done (2026-09-12). Still owed with T-083: Paddle refund events don't reach the lifecycle, so reversals never fire from live data; the affiliate-facing dashboard (clicks/sign-ups) has no data source. |
| **T-085** | Marketing site | Copy and legal drafts exist; site scaffold with copy lint being built. Compliance blocks open: legal-page drafts, sign-up risk acknowledgement, hypothetical-performance disclaimer on every paper surface. |

---

## 4. Not started

### Phase 23 — venue breadth
- **T-073** `Collector` interface + conformance test + venue registry.
- **T-074** Tier-1 venues via public bulk tickers (folded into T-065/T-066 work).
- **T-076** DEX quotes via aggregator APIs — **designed**, decomposed into T-110..T-116 below.

### Phase 23a — DEX lanes (decomposition of T-076)
Design of record: `docs/design/dex-arbitrage.md`. All signals + automatic
PAPER execution only; no wallet keys, no signing, no contract deployment,
no mempool, no bridging.

- **T-110** DEX research round → `docs/research/dex-endpoints.md`. **Needs a machine with network access** — every aggregator doc host is egress-blocked from this environment. This is the first domino.
- **T-111** `QuoteSource` interface + first aggregator + conformance test.
- **T-112** Token identity: canonical lists, `(chain_id, address)` keying, CEX contract matching, honeypot/fee-on-transfer exclusion.
- **T-113** Gas oracle + cost model + `min_notional` derivation.
- **T-114** CEX↔DEX lane (inventory model, `NetworkStatus` gating).
- **T-115** DEX↔DEX same-chain lane.
- **T-116** Paper execution, nightly report, MEV caveat, entitlement flip (`DexImplemented` → true alongside the first registered DEX venue).

### Phase 24 — strategies and unattended operation
- **T-077** Strategy registry with per-strategy paper ledger and statistics.
- **T-080** Evidence dashboard: per-strategy net PnL after fees, hit rate, drawdown, sample size against the production-gate thresholds.

### Phase 25 — SaaS
- **T-087** Client console re-skin — **DONE** (2026-09-13), integrated into `master` by fast-forward and the branch deleted; not yet pushed to origin. Implemented as: navigation, decimal presentation, Overview, Paper/Auto-Paper, Settings categories, and the pause-scope correction, with local checks green and a measured responsive/theme/zoom matrix. The e2e suite and the independent review are done: a ten-lens adversarial audit confirmed 43 findings (27 refuted) and every confirmed functional one is fixed, six of them regressions this work introduced. **Reconciliation with master is done**: master squashed its client-area work into `a8657a7` (PRs #30/#31/#32 no longer exist separately; merge base moved to `b1faf6d`), and that master is merged in at `2f9edb9` — 14 conflicts resolved by checking master's side for anything this branch lacked, 27 files auto-merged, one deliberate drop (master's Overview `reject_reason_counts` histogram, unreconcilable in scope with the session count). Four merge-introduced defects were found and fixed, three of them duplicate renderings that auto-merged cleanly and so were flagged by nothing but the test suite. Everything was re-run on the merged tree: golangci-lint 0 issues, `go test -race` pass, lint/typecheck/build clean, `npm test` 10/10, `test:unit` 82/82, e2e 68/68, route map 37 routes / 0 orphans, captures 77 stops / 0 overflow / 0 unmeasured. Integrated into `master` on 2026-09-13 by fast-forward (no second merge commit) and the branch deleted. **Published**: `origin/master` is at `1caff32` and its CI run `34719294647` completed green across all five jobs. See `docs/design/client-area-refinement-handover.md` for the limits, including two open operator questions.
- **T-088** White-label option (later phase).

### Phase 26 — production infrastructure
- **T-089** `deploy/` as code: Helm/Kustomize + Terraform; dev / paper-test / prod.
- **T-090** HA Postgres + PITR + restore drill; tick partitioning.
- **T-091** CI/CD staged deploys with canary + rollback.
- **T-092** Observability stack, SLOs, alert rules, runbooks.
- **T-093** Edge security (WAF, rate limiting), image/dependency scanning, GDPR data map.
- **T-094** Load/soak tests at target scale (venues × pairs × tenants).

### Smaller open items

- _(empty — T-062, the campaign rejection-reason histogram, closed 2026-09-12)_

---

## 5. Standing constraints that shape all of the above

- Every measurement so far is **negative**. Triangular on Binance: best
  gross +4 bps against ~40 bps of cost. Carry: see T-106. Breadth widens
  the search; it is not itself evidence that a profitable lane exists.
- No strategy may be described as profitable without a campaign report in
  `docs/campaigns/`. The only report there executed **no cycles**.
- Live execution is disabled in code (`ErrLiveTradingDisabled`) and stays
  that way until T-095's gate is met in full.
- A falling sample rate is a signal about the universe, not a reason to
  loosen the entry gates.
