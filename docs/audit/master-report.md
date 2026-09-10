# TRIANGULAR ARBITRAGE PLATFORM AUDIT

Tree audited: `master` at `abddb55` (2026-09-09). Companion documents in this
directory hold the evidence: `architecture.md`, `trading-logic-audit.md`,
`execution-risk-audit.md`, `market-data-audit.md`, `scanner-suite-audit.md`,
`database-audit.md`, `security-audit.md`, `infra-delivery-audit.md`,
`observability-audit.md`, `ui-ux-audit.md`, `test-plan.md`,
`performance-plan.md`, `implementation-roadmap.md`.

## 1. Executive summary

The platform is a single-exchange (Binance) triangular-arbitrage research and
paper-trading system with a separate multi-venue screener, a Next.js console,
Telegram control, PostgreSQL persistence and a large Helm/Terraform delivery
scaffold. Live execution is disabled by construction and every path that
could enable it was checked: none exists.

The core arithmetic is right. Leg orientation, fee placement under all three
venue conventions, quantization direction and order, depth-walk VWAP
chaining and dust accounting reproduce hand-computed cycles to the last
digit, and the platform correctly concludes that a 40 bps raw deviation is a
negative net opportunity after fees and buffers. The market-data engine
implements the Binance sequence rule exactly, fails safe on gaps, and is race
clean.

What is wrong sits one layer up. Several controls that the design documents,
the plan and the console all present as active do not exist at the system
level: no circuit breaker is ever tripped, the daily-loss and drawdown limits
never reach the risk gate, the ledger invariant is never checked, no book is
revalidated before capital is deployed, and paper fills do not look at book
health. The measurement layer has three defects that corrupt today's evidence:
the slippage metric measures buffers and size mismatches rather than
slippage, realized and unrealized PnL are conflated in opposite directions in
the live API and the database, and financial records can be dropped silently
under persistence pressure or during shutdown. Security is strong on the
execution boundary, secrets vault, CSRF and injection surfaces, and weak on
revocation and tenancy: API keys outlive disabled accounts, one proxy IP can
lock out every account, and every organisation resolves to the platform
organisation. Delivery is thorough in shape but several stages cannot run as
written (restore drill, production canary).

Verdict: **NOT READY FOR LIVE TRADING**, which is also the platform's own
declared state. As PAPER evidence, the current numbers should not be relied
on until the three P0 measurement defects are fixed and the run is repeated.

*Remediation note (2026-09-10): the three P0 defects and every P1 are
fixed and tested on the remediation branch — see "FINAL PLATFORM REVIEW"
at the end of this document for the re-measured state, per-area ratings
and the conditions that would change the verdict.*

## 2. Current architecture

One Go binary hosts every component; the hot path is a function-call/channel
path in shared memory, persistence is decoupled through an in-process outbox,
and all money is `shopspring/decimal`. See `architecture.md` §1–§7 for the
package inventory (with line counts and test presence), configuration
surfaces and data stores. Packages with no tests: `internal/execution`,
`internal/apikey`, `internal/tenancy`.

## 3. Trading flow

Exchange WS/REST → `binance.Feed` (decode, receive timestamp) → `Syncer`
(splice, U/u/pu rule) → `orderbook.Book` (single writer, health state) →
coalesced dirty set → `scanner.EvaluateTriangle` (HEALTHY pre-gate, size
search over `pricing.QuoteCycle`, buffers, `risk.Evaluate`) → bounded event
channel → `app.Engine.consumeEvents` (outbox, hub, paper queue) →
`paper.Engine.runCycle` (TTL, reservation with market-side conflict keys,
`simulation.Engine.ExecuteCycle`: seeded latency, fill-time views, limit-IOC
filter, sequential legs, exposure) → settle → portfolio → outbox cycle
record → PostgreSQL → API/console. The full stage table with locations,
concurrency, failure handling and tests is `architecture.md` §3. The Scanner
Suite runs a second, independent paper path (`architecture.md` §4).

## 4. Critical findings

1. Slippage is measured against the buffered estimate at the planned size; a
   cycle filled exactly as planned reports −10 bps and a 40 % partial fill at
   identical prices reports +15 229 bps. The value is persisted, aggregated as
   "worst slippage" and scores triangle quality. (`execution-risk-audit.md` F1)
2. Realized and unrealized PnL are conflated in opposite directions: the live
   portfolio books the entire deployed input as a realized loss when a leg
   fails (1 000 USDT loss on a ≈1 USDT event, served as "daily loss"), while
   the cycle row stores the marked total under `pnl_amount`, which the quality
   score sums as realized. (F9)
3. Financial records can be lost silently: outbox write failures are not
   counted, paper-queue drops are not counted, the persistence hook is never
   wired, shutdown cancels the outbox before in-flight cycles settle, and a
   dropped opportunity row cascades into a lost cycle row through the foreign
   key. (`database-audit.md` D3, `observability-audit.md` O1, F7, F15)

## 5. Trading logic findings

Core verified correct by execution (`trading-logic-audit.md`, worked examples
1–3). Defects: the size search is a uniform grid with ternary refinement that
misses the profitable size once `max_trade_size / min_input` exceeds ≈2 500
(safe at the shipped defaults, T1); the sizer maximises absolute profit
without regard to the impact cap and minimum edge the gate then applies
(T2); `MARKET_LOT_SIZE` is not parsed (T4); limit prices are never
tick-quantized (T5); `ValidateOrder` runs on the filled rather than the
submitted quantity (T6); fee rates are a hard-coded 10/10 bps seed with no
venue fetch (T7); topology and instrument rules are built once per run (T8);
`max_slippage_bps` is never evaluated (T9); the sizer is ~7× its documented
latency budget (T11).

## 6. Profitability calculation findings

The platform distinguishes fee-inclusive return (`GrossReturnBps`, fees in,
buffers out) from net return after latency and risk buffers, and requires a
configurable minimum net edge and minimum profit before qualification — the
safety-margin structure the audit asked for exists. Three gaps: the console
labels the fee-inclusive figure "Gross" and itemises neither fees nor buffers
(`ui-ux-audit.md` F3); no realization ratio (realized ÷ expected) is computed
anywhere (`observability-audit.md` O9); fees paid in intermediate assets are
never valued, so the reported fee bill is a fraction of the real one (F13).

## 7. Market data findings

Sequence validation, splice, gap handling, chaos coverage, single-writer
discipline, immutable views, weight budgeting and replay determinism are all
correct (`market-data-audit.md`). Defects: reconnect backoff never resets and
gates the planned 24 h reconnect, so feed blackouts recur and worsen (M1);
the documented clock manager does not exist and the clock-safety gate is
permanently passed (M2); replay never evaluates STALE (M3); no crossed-book or
non-positive-price check (M4); resync goroutines outlive their session (M5).

## 8. Execution findings

Sequential legs with fill-time book reads, limit-IOC filtering, partial fills
feeding the actual output forward, exposure on failure, seeded determinism
and TTL checks are implemented. Missing: revalidation of book versions and
risk before leg 1 (F4); fill-time health checks (F5); an ABORTED outcome and
a staged shutdown (F7); persisted expected-vs-actual per cycle (F14); the
configured concurrency cap is not applied to the executor (F11); no unwind
path and an optimistic exposure mark (F16).

## 9. Partial fill / recovery findings

Partial and zero fills are modelled correctly and re-validated against
min-qty and min-notional on the next leg. Recovery is the gap: a restart
rebuilds the ledger from seed balances under a new session (F8, D4); in-flight
cycles settle as TIMEOUT and may lose their rows (F7); a concurrent duplicate
reservation key is accepted rather than rejected (F10); no
client-order-id/idempotency design exists for any future submission path.

## 10. Risk management findings

Evaluated limits: triangle disabled, breaker open, clock, book state, book
age, age spread, data quality, min edge, min profit, max trade size,
triangle capital, utilization, concurrency, price impact, TTL. Dead: daily
loss, drawdown (never fed), max slippage (never checked), clock (never
flipped), breaker open (never tripped). No breaker trigger of the thirteen
documented is implemented; the ledger invariant is never checked; the
emergency control is paper pause (stops new cycles, lets in-flight settle),
reachable only from the Paper page and Telegram.

## 11. Backend findings

Package boundaries are clean and the hot path is free of database, AI,
Telegram and network calls. Structural issues: two independent paper
execution stacks (`internal/simulation` + `reservation` + `portfolio` versus
`internal/screener/paperexec`, F17, T10); dead schema (`virtual_balances`,
`balance_snapshots`, `pnl_snapshots`); `cmd/worker`, `cmd/recorder`,
`cmd/replay` build no components; no retention job; strategy parameters fall
back to in-memory defaults on load failure (I6); a shared 8-connection pool
with no statement timeouts (D8); one goroutine without an owner in the engine
(execution §9).

## 12. Security findings

Verified: execution boundary, closed secrets registry, vault crypto and
write-only handling, Paddle webhook verification and idempotency, complete
CSRF coverage, session and password handling, SSRF hardening, parameterised
SQL, path-traversal guards, prompt-injection boundaries, Telegram callback
binding, no committed secrets, non-root containers, console CSP.
Defects: login throttle on the proxy address (S1); `smtp_url` leak through a
parse error into logs, database and an API response (S2); API keys survive
disable and membership removal (S3); tenant isolation never engages (S4);
VIEWER reads platform settings, secrets inventory, config and metrics (S5);
bootstrap admin re-upserted on every boot (S6); platform-global screener
settings writable by any organisation ADMIN (S7, D1); provider secrets
writable by a promoted ADMIN (S8); audit immutability unenforced (S9); member
add without consent (S10); WebSocket topics unauthorised (S11).

## 13. Performance findings

Metrics on the hot path are atomics and off-path histograms with measured
cost; `go test -race` is clean across the trading path. Concerns:
`SizeSearch` at ≈7.3 ms and 108 k allocations per triangle evaluation
against a "sub-millisecond" comment (T11); the reservation mutex sits on the
evaluator path; `Registry.AnyOpen` allocates per evaluation; the screener's
per-event ledger scans and real sleeps inside the automation tick (X9).
See `performance-plan.md`.

## 14. UI findings

No P0: paper is never presented as live and no pre-fee figure is labelled
profit. P1: the mode banner is absent from the mobile chrome (F1); there is
no emergency control beyond a secondary Pause button 2–3 clicks deep (F2);
"Gross" labels a fee-inclusive figure and negative nets are untoned (F3);
cycle-outcome vocabulary does not match the backend, so stranded cycles look
benign (F4); the overview fails the five-second test on PnL, breakers and
feed state (F5); the Paper page is not a live-cycle monitor (F6); request
failures render as empty states (F7); a persisted rule threshold is
float-divided (F8).

## 15. UX findings

History dead-ends at `/orders?cycle=` with no cycle timeline (F13); strategy
parameters lack units, defaults and implications, and two fields mix
fraction inputs with percent help (F11); the Risk Center never places
observed values beside their limits (F12); no data-age indicator on polled
money tables and transient errors blank the table (F9); accessibility gaps in
chips, dialogs, tables and forms (F15); decision-critical text hidden in
tooltips (F16); navigation dead ends (F17).

## 16. Observability findings

43 series exported with bounded labels and benchmarked cost; dashboards and
rules reference real series. Missing: order/cycle latency histograms (O3),
reason and outcome labels (O5), outbox/paper drop counters and queue depths
(O1, O8), realization ratio (O9), paper `Received`/`Skipped` (O6), any panel
or alert on slippage (O7) or screener venue health (O10); the breaker metric
and alert are structurally dead (O2); no spans (O11).

## 17. Testing gaps

See `test-plan.md` for the coverage map and tool output. Headline gaps: no
tests for `internal/execution`, `internal/apikey`, `internal/tenancy`; the
slippage test pins the defective −5 bps behaviour; no test exercises a breaker
trigger, the loss limits, revalidation, fill-time book health, the shutdown
ordering, the reconnect backoff, the sizer at large ranges, the Binance USDC
collision, the 500-lane cap, or the data-age flapping.

## P0 Issues

- P0-1 Slippage metric (execution F1, trading T3)
- P0-2 Realized/unrealized conflation (execution F9)
- P0-3 Silent loss of financial records (database D3, observability O1, execution F7/F15)

## P1 Issues

See `implementation-roadmap.md` P1-1 … P1-24 (breakers, loss limits,
revalidation, fill-time health, invariants, restart persistence, reconnect
backoff, clock manager, sizer range, login throttle, `smtp_url`, API-key
revocation, tenancy, bootstrap admin, backups, compose exposure, canary,
retention, audit immutability, Binance perp collision, lane cap, data-age
flapping, latency/label metrics, console P1s).

## P2 Issues

See `implementation-roadmap.md` — 60 items across trading precision,
execution, market data, security hardening, delivery, database, scanner,
observability and console.

## P3 Issues

See `implementation-roadmap.md` — hygiene, naming, documentation drift and
polish.

## Recommended Architecture

Keep the monolith and the in-process hot path; they are sound. Change five
things: (1) make the breaker registry a real actor — book transitions, loss
limits, invariant breaches, persistence failures and queue saturation trip
it, and both the scanner and the paper engine gate on it; (2) put a
revalidation step between RESERVED and SIMULATING that re-quotes at the
reserved size and re-runs the risk gate against fresh views; (3) give the
paper session durable state — balances, exposure, realized PnL and drawdown
snapshotted through the outbox and resumed on boot — with a staged shutdown
that settles cycles before the outbox closes; (4) unify the two paper
execution stacks on `internal/simulation` + `reservation` + `portfolio` +
`exchange.InstrumentRules`; (5) make tenancy real — explicit organisation
assignment, org-scoped screener settings and reports, org-aware RBAC.

## Recommended Trading Flow

Exchange → feed (backoff reset on stable session, session-scoped resyncs,
clock manager) → book (crossed/negative checks; transitions trip the feed
breaker) → evaluator (breakpoint-aware sizer constrained by the gate's own
limits; venue-fetched fees; periodic metadata refresh) → risk gate (loss and
drawdown fed from the portfolio; slippage limit evaluated) → paper engine
(breaker gate → reserve → revalidate → execute against HEALTHY-only fill-time
views → settle → invariants → persist realized and mark separately → metrics
with outcome labels and latency histograms) → durable session state →
console with an always-visible mode banner, a one-click pause with stated
consequences, honest gross/net/buffer labels, backend outcome vocabulary and
a live cycle monitor.

## Implementation Order

1. P0-1 slippage baseline; 2. P0-2 realized/mark separation; 3. P0-3 drop
counters, persistence breaker, staged shutdown, stub-row cascade fix;
4. fill-time health (P1-4); 5. revalidation (P1-3); 6. invariants (P1-5);
7. breaker triggers and paper gate (P1-1); 8. loss/drawdown wiring (P1-2);
9. sizer range (P1-9); 10. reconnect backoff (P1-7); 11. login throttle,
`smtp_url`, API-key revocation, bootstrap admin (P1-10/11/12/14); 12. tenancy
preference and platform-global settings gate (P1-13); 13. metrics labels and
latency histograms (P1-23); 14. Binance USDT filter, lane cap, data-age hold
(P1-20/21/22); 15. console P1s; 16. compose exposure and canary assertion
(P1-16/17); 17. restart persistence, retention, audit immutability, backups,
clock manager (P1-6/8/15/18/19) — larger changes needing operator decisions,
scheduled after this branch.

## Critical profitability statement

Nothing in this audit or the platform's own reports shows a positive net
edge after fees, slippage and buffers on Binance triangular cycles; the
platform's own campaign evidence is negative (best gross +4 bps against
≈40 bps of costs). No claim of profitability is made or supported. The
engineering objective is to make the measurement honest first — which the
P0 fixes address — and to protect capital by making the advertised controls
real.

## FINAL PLATFORM REVIEW (remediation branch, 2026-09-10)

Reviewed tree: `claude/triangular-arbitrage-platform-969nkv` at `10e1ec0`
(35 commits after the audited `abddb55`), measured on this host with the
repository's own suites and benchmarks. What follows rates the platform
AS IT NOW STANDS; the body of this document above remains the audit of
`master` at `abddb55` and is unchanged.

### Verification performed for this review

- `gofmt -l internal cmd` clean; `go vet ./...` clean;
  `golangci-lint run ./...` (standard + gosec + misspell + unconvert +
  copyloopvar) — 0 issues.
- `go test -race -count=1 ./...` — all packages pass (no database
  variables; database-backed tests skip).
- `ARB_TEST_DATABASE_URL=… ARB_TEST_DB_DESTRUCTIVE=1 go test -race
  ./internal/storage/...` against a disposable PostgreSQL 16 with
  migrations 000001–000021 applied — pass.
- `web`: `npm run lint` (eslint + 54/54 contrast checks),
  `npm run typecheck`, `npm run build` — pass.
- Playwright against a real `arbd` (PAPER mode): **47/47 tests pass**,
  including the four added for F5/F6 and the breaker acknowledgement.
- Sizer benchmarks (see `performance-plan.md` for the table): the exact
  path allocates **12 048 objects per triangle evaluation** versus the
  audited search's **108 231** (9.0× fewer; 7.8× faster on the same
  host), and the 3000:1 reproduction returns the optimum in one
  evaluation.

### Ratings per area (X/10, evidence in the linked sections)

| Area | Was | Now | What moved |
|---|---|---|---|
| Trading logic & arithmetic | 8 | 9 | T1/T2 (breakpoint-aware, gate-constrained sizer), T4/T5/T6 filter semantics fixed; T12 precision cluster (P3) and T10 screener duplication remain |
| Profitability honesty | 5 | 8 | P0-1 slippage vs plan, P0-2 realized/mark separation, F13 fee valuation, F3 labelling; O9 realization ratio still absent |
| Market data | 7 | 9 | M1–M5 fixed (backoff reset, clock manager, replay staleness, crossed-book corruption, session-scoped resyncs); M6/M7 hygiene remain |
| Execution simulation | 5 | 8 | F4 revalidation, F5 fill-time health, F7 ABORTED + staged shutdown, F10/F11/F12/F14/F16 fixed; no unwind path (documented exposure-mark model), F17 dead code remains |
| Risk controls | 3 | 8 | P1-1 breakers trip for real (feed, persistence, loss, drawdown, slippage, inconsistency, metadata), P1-2 limits fed, P1-5 invariants halt the engine, operator acknowledgement endpoint + Risk Center control; pre-trade slippage stays post-hoc by design |
| Persistence & database | 5 | 7 | P0-3 no silent loss (counted drops/refusals/unlinks, persistence breaker), P1-6 ledger resume, P1-18 retention, P1-19 immutability; D7–D10 (indexes, CHECKs, pool sizing) remain |
| Security | 6 | 7 | S1–S4 (+ the CreateUser follow-up), S6, S9 fixed; S5 (VIEWER breadth), S8, S10 (consent), S11 (WS topics), S12–S15 remain |
| Observability | 5 | 7 | O1/O2/O3/O5/O6/O8 closed — drop/refusal counters, live breaker metric, latency histograms, outcome/reason labels, queue depths; O7/O9/O10/O11 remain |
| Infra & delivery | 5 | 6 | P1-16 compose exposure, P1-17 canary guards, P1-18 worker, P1-19 done; P1-15 backup automation is an open operator decision (framed in `docs/decisions/`), I5–I15 partially |
| Scanner Suite | 6 | 7 | P1-20/21/22 fixed (perp collision, lane cap, data-age hold with close reasons); X4/X5/X7/X8/X9 and the P3 cluster remain |
| Console & UX | 5 | 7 | F1–F8 all done (mode banner, one-click pause, honest labels, outcome vocabulary, five-second overview, live-cycle monitor, error states, exact decimals); F9–F20 (P2/P3) remain |
| Testing | 6 | 8 | 681 → 841 Go test functions (+160), 47 e2e tests (was 43, two of them failing on the audit host); every fix carries its test; the known gaps (frontend unit layer, down-migration replay, npm audit gate, fuzz) remain |

### Problems fixed since the audit (headline list)

All three P0 measurement defects (slippage baseline, realized/unrealized
conflation, silent record loss) and all 24 P1 items are closed — P1-15's
engineering half (drill fails closed, never writes to production) with
the provider choice framed as an open operator decision, and P1-24 fully
(F5 and F6 landed in the remediation tail). From P2: T2, T4, T5, T6, T8
(metadata-diff breaker), F10, F11, F12, F13, F14, F16, M3, M4, M5, O6,
O8, the S4 `CreateUser` org-placement follow-up; T7 is resolved by a
recorded decision rather than code (see below). The breaker
acknowledgement gap the audit's own remediation plan called out — an
operator-closed breaker with no API to close it — is closed with
`POST /api/v1/risk/breakers/close` (ADMIN, CSRF, audited,
type-to-confirm) and a Risk Center control.

### Remaining risks (why nothing here is a profitability claim)

1. **No positive edge exists in evidence.** The platform's own campaign
   evidence is negative (best gross +4 bps against ≈40 bps of costs);
   nothing in this remediation changes the economics, only the honesty
   of the measurement. The fee schedule remains operator-configured
   (T7 decision record in `docs/research/fees.md`): a stale rate or a
   missed promo expiry silently shifts every figure, and per
   `triangular-constraints.md` §"one wrong fee assumption invalidates
   every result".
2. **Backup/restore is unproven on the managed tier** until the
   operator picks an option in `docs/runbooks/restore-drill.md` and a
   verify drill passes (P1-15 open decision).
3. **Topology changes still require a restart** — T8 detects them and
   stops execution (the metadata_changed breaker re-arms while the diff
   persists) but nothing rebuilds a running graph.
4. **Two paper stacks remain** (`internal/simulation` and the
   screener's `paperexec`, F17/T10): the screener's fill model can drift
   from the engine's exact one.
5. **Security remainder:** VIEWER breadth on platform surfaces (S5),
   promoted-ADMIN writes to provider-group secrets (S8), member-add
   without consent (S10), unauthorised WebSocket topics (S11), Argon2
   clamp and HTTP server timeouts (S12/S13), unpinned CI and `npm
   audit` highs (S14).
6. **Operational polish:** the reservation mutex and `AnyOpen`'s
   per-evaluation allocation still sit on the evaluator path
   (performance plan items 2–3), `internal/api`'s race suite takes
   ≈7.7 min serially, and the scanner's per-event ledger scans and real
   sleeps (X9) remain.

### Tests added by the branch

160 Go test functions (681 → 841) and 4 e2e tests (43 → 47, all green)
covering: slippage baselines and partial fills; realized/mark
separation; drop/refusal/unlink counting; staged shutdown and ABORTED
settlement; revalidation refusal paths; fill-time book health; ledger
invariants and resume; breaker trips (feed, loss, drawdown, slippage,
inconsistency, metadata) and the operator close; reconnect backoff
reset; the clock monitor; the breakpoint sizer at 3000:1 and the
constrained objective; filter semantics (submitted-quantity validation,
market lot size, tick quantization); exposure liquidation marks; tenancy
scoping and the CreateUser org placement; retention and immutability;
the live-cycle registry and its leg-stage hook; cycle reason/fees
persistence; the console's five-second overview, live-cycle monitor and
breaker-close flow.

### Measured before/after (headline)

- Sizer: 108 231 → 12 048 allocs/op (9.0×), 6.8–8.1 ms → 0.31 ms on the
  audit-class comparison benchmark (7.8× on this host); the profitable
  size is found in one evaluation where the old grid returned a loss.
- One exact quote (`QuoteCycle50Levels`): 138 µs (audit host) → 30 µs
  (this host), 1 381 allocs.
- e2e: 43 tests / 2 environment-bound failures → 47 / 0.
- No measurable hot-path regression from the new monitors: the metadata
  check is one bounded goroutine per run at one public REST call per
  hour; the live-cycle registry updates are mutex-guarded map writes off
  the pricing path; progress events are lock-read + no-op when unset.

### Verdict

**NOT READY FOR LIVE TRADING.** Live submission remains disabled by
construction (`LiveExecutor` refuses every call) and no evidence of a
positive net edge after fees, slippage and buffers exists — the
platform's own campaigns say the opposite. The measurement layer is now
honest, the advertised controls are real and tested, and the PAPER
evidence this branch produces can be trusted as evidence. The verdict
changes to "READY FOR CONTROLLED LIVE VALIDATION" only when all of the
following hold: a `docs/campaigns/` report shows a positive net edge
under the exact fee/depth/quantization model with its verdict stated; a
backup option is chosen and a verify drill has passed; S5/S8/S10/S11 and
the operator-decided items in the roadmap's P2 security block are
closed; and the production-execution-gate review (legal, compliance,
insurance) required by `docs/design/crypto-arb-platform-command.md`
RULES (c) is recorded in `docs/decisions/`. No claim of profitability is
made or supported by this review, and none should be inferred from the
engineering scores above.

### Addendum: the blocking items (this branch, 2026-09-10 later the same day)

The review above described the merged remediation branch. Its verdict
conditions named four things engineering could still close; all four are
closed on this branch, with the updated ratings and evidence:

- **S5** — the platform settings document (and its versions and preview
  dry-run), the secrets inventory, and the shared-mux `/metrics` now
  answer to `platform_admin` only; strategy config reads moved to
  `scanner:config` (read what you can edit — OPERATOR keeps the page,
  VIEWER loses it); the capabilities route strips the vault status and
  the Telegram status strips the allowlist for non-platform-admins.
- **S8** — every secret write and the whole inventory (not just the
  exchange group) is `platform_admin`-only, and `UpdateUserRole` moves
  `platform_admin` with the role exactly as `CreateUser` does at insert,
  so a promoted ADMIN is never a quiet non-admin and a demotion never
  keeps the flag.
- **S10** — `POST /org/members` refuses accounts that already hold any
  membership (this org included), naming the consented routes (operator
  placement at creation, the T-085 invitation flow); roster e-mails are
  manager-only; the org API-key inventory is manager-only.
- **S11** — WebSocket subscribes are authorized per topic against the
  same permissions the REST routes enforce, refused subscribes get an
  explicit error frame, unknown topics fail closed, unsubscribe stays
  free.
- **P1-15** — decided and implemented: option B (recorded with evidence
  in `docs/decisions/2026-09-10-backup-automation-option-b-logical-dump.md`):
  a daily `pg_dump -Fc` CronJob into the object-locked bucket plus a
  `pgdump` drill source. Exercised locally: guards hold, the archive
  restores, the write-probe fails as required, all five checks pass
  (`success=1 source=pgdump`), and the drill refuses without the
  disposable flag. The exercise also caught and fixed a real defect —
  `verify.sql`'s `CASE … 1/0` checks errored on every run because
  PostgreSQL constant-folds constant division in a not-provably-dead
  CASE arm; they are DO/RAISE blocks now.

Ratings updated: **Security 7 → 8** (S5/S8/S10/S11 closed; S12–S15 P3
remain), **Infra & delivery 6 → 7** (the backup decision made, the drill
proven; I5–I15 partially). Verification for this addendum: gofmt/vet/
golangci-lint clean, `go test -race ./...` green, the storage suite
green against a disposable PostgreSQL 16 (000001–000021), web
lint/typecheck/build green, Playwright 47/47.

Fresh campaign evidence was produced on the remediated engine the same
day (`docs/campaigns/01M25GET9C8XVKD9358JT15BNC/`): a 5 m 15 s live
Binance recording, 24 scenarios, **zero qualified opportunities** — the
fee wall rejects the entire opportunity space at Regular tier in that
window, consistent with every prior campaign. No profitability claim is
made; the run demonstrates the honest negative end to end.

Of the verdict's original conditions, two remain, neither an engineering
gap: a campaign showing a positive net edge (the market has not
provided one), and the production-execution-gate review (legal,
compliance, insurance) recorded by the operator. The verdict stays
**NOT READY FOR LIVE TRADING**.
