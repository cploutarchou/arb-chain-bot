# Resource: Testing

Authoritative documents: SKILL.md §69–§73, `docs/risk.md` §8,
`docs/security.md` §10.

Structure:
- Unit tests live beside code (`*_test.go`); integration tests under
  `tests/integration` (build tag `integration`, dockerized Postgres);
  chaos/replay fixtures under `tests/fixtures` (recorded frame segments);
  E2E under `web/e2e` (Playwright; Chromium preinstalled at
  /opt/pw-browsers — never run `playwright install`).
- Table-driven with hand-computed decimal expectations for all financial
  math (SKILL.md §70 cases are mandatory coverage: bid/ask inversion,
  rounding, fee in base/quote, min qty/notional, truncation, depth
  insufficiency, stale/corrupt books, top-of-book-profitable-but-net-
  negative, leg failures, duplicate/expired opportunities, capital races,
  reservation conflicts).
- `go test -race ./...` green is a merge gate; concurrent packages get
  dedicated race storms; property tests (conservation, monotonicity,
  round-trips) where invariants exist.
- Market-data chaos (SKILL.md §71): fault injection through fakes —
  disconnect, reconnect storm, duplication, loss, out-of-order, snapshot
  delay, REST failure, WS freeze, clock skew, burst; assert safe-fail
  (health transitions, breaker opens, no qualification on bad data).
- Replay determinism: same recording + config + seed ⇒ byte-identical
  decision log.
- API tests: real router, fake deps, RBAC denial matrix, error schema.
- Frontend: component tests + typecheck; Playwright E2E critical paths
  (SKILL.md §72).
- Flaky tests are defects — root-cause them; no sleeps for
  synchronization, no skipped/quarantined tests to get green.
- Benchmarks (SKILL.md §73) with -benchmem for book apply, re-price,
  depth walk, size search, serialization, fan-out; regressions are
  findings.
