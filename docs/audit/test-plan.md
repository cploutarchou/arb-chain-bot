# Test coverage audit and test plan

Audited tree: `master` at `abddb55` (2026-09-09). All commands below were run
in the audit environment; DB-backed storage tests skip without
`ARB_TEST_DATABASE_URL` (CI provides a real PostgreSQL 16 service and runs
them there).

## Tool results

| Command | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `gofmt -l cmd internal` | clean |
| `go test ./...` | all packages `ok`; no test files in `cmd/*`, `internal/apikey`, `internal/execution`, `internal/tenancy` |
| `go test -race ./...` | all packages `ok` in ≈5.5 min (`internal/api` 315 s, `internal/auth` 40 s, `internal/screener/venue` 25.5 s); no race reported |
| `golangci-lint run ./...` (v2.5, `gosec`, `misspell`, `unconvert`, `copyloopvar` on top of `standard`) | 0 issues |
| `cd web && npm ci` | ok; `npm audit`: 3 vulnerabilities — `js-yaml` 4.0.0–4.3.1 (high, CPU DoS), `postcss` ≤ 8.5.22 (high, XSS / path traversal via source maps, transitively through `next`), 1 moderate |
| `npm run lint` | pass (54/54 contrast checks) |
| `npm run typecheck` | pass |
| `npm run build` | pass, 36 routes |
| Playwright E2E | not executed here; wired through `scripts/e2e.sh` against a real `arbd` in PAPER mode on `:18080` with the fake advisor, `workers: 1`, `retries: 0`; one spec file `web/e2e/console.spec.ts` with 40 cases |

Coverage (`go test -cover ./internal/...`): 0 % in `internal/execution`,
`internal/apikey`, `internal/tenancy` (no in-package tests; the last two are
exercised transitively through `internal/api`); `internal/app` 50.2 % and
`internal/exchange/binance` 54.2 % are the lowest non-DB-gated packages;
`internal/storage` reads 0.5 % locally only because its tests are DB-gated.

## Coverage map

| Area | Tests | Status |
|---|---|---|
| Price conversion, bid/ask orientation, both directions | `pricing_test.go: TestBuyLegTwoLevelsReceivedFee, TestSellLegReceivedFee, TestFullCycleHandComputed`; `graph_test.go: TestEnumerateBothDirectionsFromOneStart` | covered |
| Reverse pairs | same enumeration test (ETHBTC consumed as BUY in one direction and SELL in the other) | covered |
| Taker fee per convention | `fees_test.go: TestPlacementTable, TestUsableInput, TestNetOutput`; `pricing_test.go: TestSellLegSpentFeeInput, TestBuyLegQuoteFeeInput` | covered |
| Quantization direction, tick/step | `instrument_test.go: TestQuantizeQtyStep, TestQuantizeQtyDecimals, TestQuantizePriceDirections` | covered |
| Min qty / min notional | `instrument_test.go: TestValidateOrderBoundaries, TestQuantizeThenValidateInteraction`; `pricing_test.go: TestMinNotionalViolationSurfaces, TestDustInputFails` | covered |
| Triangle enumeration | `graph_test.go` (both directions, multiple starts, untradeable/self-loop, bad rules, foreign exchange, larger universe) | covered except two distinct markets over the same pair (NO TEST) |
| Depth-walk VWAP and exhaustion | `pricing_test.go: TestProfitableTopOfBookUnprofitableAfterDepth, TestInsufficientDepthSetsLiquidityLimited, TestZeroDepthFails, TestOptimalSizeSearch` | covered at small ranges; NO TEST at large `max/min` ratios (trading T1) |
| Sequential chaining on actual output | `pricing_test.go: TestLegConservation`; `paper_test.go: TestAllFilledReproducesPlan, TestLeg1PartialFill` | covered |
| Partial and zero fills | `paper_test.go: TestLeg1PartialFill, TestMoveBeyondToleranceRejects, TestLeg2FailureCreatesExposure, TestLeg3FailureCreatesExposure` | covered |
| Stale-opportunity rejection | `opportunity_test.go: TestExpiryRules`; `risk_test.go` TTL case; `paper_test.go: TestExpiredPlanShortCircuits` | covered at three of four layers; `paper.Engine`'s own skip branch untested |
| Revalidation before execution | `opportunity_test.go: TestRevalidationContract` (primitive only) | NO PRODUCTION CALLER (execution F4) |
| Reservation idempotency and races | `reservation_test.go: TestIdempotentReserve, TestConcurrentDuplicateKey, TestConflictKeysSerializeTriangles, TestConcurrentStormInvariants`; `paper/engine_test.go: TestConcurrencyBounded, TestSameTriangleSerializedByConflictKeys, TestDuplicateOpportunityRunsOnce` | covered (concurrent duplicate of an ACTIVE key is the untested shape, execution F10) |
| Risk limits at exact boundaries | `risk_test.go: TestAllowsAtExactBoundaries, TestRejectionsPerLimit, TestAllChecksRecordedEvenAfterFailure, TestOverridePrecedence, TestMonotonicity` | covered |
| Circuit breakers | `risk_test.go: TestBreakerLifecycle` (framework only) | NO TEST of any production trigger (none exists) |
| Emergency stop | paper pause: `paper/engine_test.go: TestPausedEngineSkips` | no distinct feature; no E2E click-through of Pause/Resume |
| PnL incl. non-start-asset fees and exposure | `paper_test.go` failure/timeout tests; `portfolio_test.go: TestReconciliationAcrossCycles, TestEquityAndDrawdownWithMarks, TestBookMarkerInversePair, TestDailyLossOnlyCountsLosses` | covered (the slippage test pinned the defective −5 bps value until this branch) |
| Sequence gap / snapshot splice | `orderbook/book_test.go: TestSequenceGapCorruptsBook, TestDuplicateDropped`; `binance_test.go: TestSyncerOfficialSpliceFlow, TestSyncerSnapshotBehindBuffer, TestSyncerBufferOverflow, TestSyncerGapTriggersResync` | covered |
| Reconnect and resubscribe | `chaos_test.go: TestChaosReconnectStorm, TestChaosWSFreeze, TestChaosRESTFailureDuringResync`; supervisor tests for dial retry | covered at the syncer level; NO TEST drives `Feed.Run` through disconnect → backoff → reconnect (market data M1) |
| Rate limiting | `weight_test.go: TestRESTGateWindowAndBan`; venue `TestGateHonoursRetryAfter`, `TestHTXRequestLimitEnvelopeBacksOffTheGate`, `TestMEXCInBandRateLimit510`; `apikeysapi_test.go: TestAPIKeyRateLimit429` | covered |
| Restart recovery of in-flight cycles | supervisor pause/running/grace tests; `TestSupervisorRestartRebuildsTopologyWithRealEngine`; `TestCancelMidCycleAbortsWithExposure`, `TestShutdownStagedCancelsWritersOnlyAfterProducersReturn`; ledger resumption: `TestPlanResumeRestoresLedgerAndFoldsReserved`, `TestLedgerSnapshotRoundTripRestoresPortfolio`, `TestLedgerSnapshotRoundTripAndResumableSession` (DB); `TestInvariantViolationPausesEngine` | covered at each layer; an end-to-end test of a real restart with a live in-flight cycle against a database remains open |
| Property tests | `TestMonotonicity`, `TestLegConservation`, `TestConcurrentStormInvariants`, quality-score monotonicity tests | covered |
| Replay determinism | `backtest_test.go: TestRunIsDeterministic`; `paper_test.go: TestDeterminismBySeed`; `marketdata_test.go: TestReplayDeterminism` | covered |
| Frontend component tests | none (no test runner in `web/package.json`) | NO TEST |
| E2E | login, dashboard, config, paper (honest states, reset disabled while running), opportunities, history, settings, RBAC gating, screener conversions, reports | pause/resume click-through and any emergency control missing |

## Hygiene

- No `t.Parallel()` anywhere; the suite runs serially (`internal/api` 120–315 s).
- 43 test files use `time.Sleep`, almost all as poll-until-condition loops; one fixed sleep asserting a negative outcome (`internal/ai/switch_test.go` ≈111–137).
- `internal/marketdata/marketdata_test.go: TestTornSegmentTail` has a data-dependent `t.Skip` on its core assertion (currently passes; a zstd bump could silently skip it forever).
- Three permanently skipped tests in `internal/entitlements/capabilities_test.go:17,38,76`.
- No `func Fuzz*` anywhere.
- Financial tests use hand-computed decimal fixtures with the arithmetic in comments; chaos tests construct protocol-accurate synthetic frame sequences.

## CI gating (`.github/workflows/ci.yml`)

Blocking: gofmt, vet, build, migrations up against `postgres:16-alpine`, `go test -race ./...` with the DB, golangci-lint (gosec on), govulncheck (`@latest`, unpinned), frontend lint/typecheck/build, gitleaks, Playwright against a real backend. Missing: `npm audit`, down-migration replay, container scan, fuzz, per-package coverage trend, action SHA pins, `timeout-minutes`. Whether the jobs are required checks is repository-settings state, not visible in the tree.

## Findings (ranked)

1. **P1** Revalidation-before-execution is an inert primitive with no production caller (execution F4).
2. **P1** No frontend unit or component layer; all confidence rests on one E2E file. Introduce Vitest + React Testing Library for `web/src/lib` (percent/fraction helpers, API client mapping) and components; add a CI step.
3. **P2** No distinct emergency control and no E2E click-through of Pause/Resume (ui F2).
4. **P2** No crossed-book detection or test (market data M4).
5. **P2** `internal/execution` (`Outcome.Complete()` over all outcomes), `internal/apikey` (`ValidateScopes` error branches, `ErrPrefixCollision`), `internal/tenancy` have no in-package tests.
6. **P2** No enumeration test for two distinct markets over one pair.
7. **P2** CI never applies `*.down.sql`; add up → down (reverse) → up against the same disposable service.
8. **P2** `npm audit` is not a gate; two high advisories are live.
9. **P2** No test combines an in-flight cycle, a real supervisor restart and the ledger invariant.
10. **P3** Data-dependent skip in `TestTornSegmentTail`; permanently skipped entitlement tests; no fuzz targets (`QuantizeQty`, ladder apply, frame decode); paper engine's TTL-skip branch untested; coverage-trend check per package.

## Tests added or required by this branch

Every fix commit on this branch carries its own test; the required new
tests are listed against each finding in `implementation-roadmap.md`. The
ones that prove the P0/P1 defects: zero slippage on identical books and on
a proportional partial fill; a STALE book at fill time fails the leg; a
moved or degraded book between qualification and reservation releases the
hold; a breaker trip from a book transition or a loss-limit breach stops
qualification within one evaluation and pauses the paper engine; the
ledger invariant runs after settlement and halts on breach; a stable feed
session resets the reconnect backoff; the sizer finds a known breakpoint at
`max/min` ratios up to 10⁶; the Binance USDT perp survives a USDC
contract on the same base.
