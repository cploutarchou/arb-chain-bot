---
name: console-feature
description: 'Workflow and conventions for adding an operator-facing feature to the arb-chain-bot console end to end — DB-backed settings, Go API with RBAC/CSRF/audit, Next.js page, e2e, review. Use for any "manage X from the UI" request (settings, providers, venues, users, jobs, secrets). Use when the request mentions: "manage from the UI", "add a settings section", "store in the database instead of env", "new console page", "expose endpoint to the console", "enable/disable X from the console".'
---
# Console feature workflow

Feature: the task the user named (a slash command passes it as its arguments).
This project is a paper-only triangular-arbitrage research platform.
Non-negotiables that every console feature inherits (SKILL.md
`triangular-arbitrage-platform`): live trading stays disabled
(`LiveExecutor` → `ErrLiveTradingDisabled`, no LIVE mode anywhere), no
exchange trading keys are stored or accepted, risk-engine and
decimal money-math code are never edited as a side effect, and nothing
is described as guaranteed or risk-free.

## 1. Locate the seams before designing

- Boot-time settings: `internal/config/config.go` (env) → seeded once
  into `internal/platform` (versioned, DB-backed, restart-scoped) or
  `internal/strategy` (hot-swappable params). New operator settings go
  into one of those two documents — never a new ad-hoc table.
- Restart-scoped changes apply through `internal/app/supervisor.go`
  (guard rails: campaign/replay running, recording active).
- Secrets: write-only vault (`internal/secrets`, if present) under
  `ARB_SECRET_KEY`; values are never returned or logged. Never type or
  handle secret values in a session; the operator enters them.
- Background jobs follow `internal/campaign.Runner` (single-flight,
  persisted row, progress on a hub topic, orphan reconciliation at
  start).

## 2. Backend contract first

1. Add/extend the settings document with validation in the package's
   `Validate()`; declare `field_timing` (hot vs restart) there — the
   frontend never hardcodes timing.
2. API in `internal/api/<area>api.go`: interface-typed `Server` field
   (so tests use a fake), wrapper order
   `requirePerm(perm, requireCSRF(handler))` for every mutation,
   `http.MaxBytesReader` caps, `DisallowUnknownFields`, error codes in
   the `{data, error:{code,message,correlation_id}}` envelope, `audit()`
   on every mutation, secrets never in responses or logs.
3. Permissions come from `internal/auth/rbac.go` (add a constant and the
   role rows; ADMIN for venues/fees/system/secrets/users, OPERATOR for
   operational controls) and the mirror in `web/src/lib/auth.tsx`.
4. Migrations: next `migrations/NNNNNN_name.{up,down}.sql`; down scripts
   use `IF EXISTS`; add new tables to the truncate list in
   `internal/storage/storage_test.go`. Storage tests run only against a
   disposable Postgres you start yourself — never the dev container on
   :5432.
5. Tests: unit for validation/state machines, handler tests with the
   `newTestServer`/`login` helpers (RBAC, CSRF, 409/400 paths), `-race`.

## 3. Frontend

- Reuse `web/src/components/ui.tsx` (`Section`, `Stat`, `Table`,
  `Badge`, `Button`, `ConfirmDialog`, `DiffTable`, `Unavailable`),
  `usePoll`, `connectHub` for live topics, `api.*` in
  `web/src/lib/api/client.ts` with strict types (money and bps as
  decimal strings; nullable Go slices typed as `T[] | null`).
- Flow for settings: edit → preview → `DiffTable` in `ConfirmDialog` →
  apply (with `parent_version`) → versions/rollback table; timing chips
  from the backend; verbatim backend messages; honest empty and
  unavailable states (no env-var or shell language in copy).
- Verdicts and risk outcomes are rendered verbatim, never softened.
- Gate mutations with `can(role, "<perm>")`; render read-only otherwise.
- Update `web/e2e/console.spec.ts` (page render + one happy path); run
  `scripts/e2e.sh`; `npm run lint && npm run typecheck && npm run build`.

## 4. Finish

- `gofmt`, `go vet ./...`, `go test -race ./...`, golangci-lint clean.
- Docs: `docs/deployment.md` (operator how-to), `docs/architecture.md`
  (tables/topics), `docs/MASTER_PLAN.md` (task status, honest).
- Run the `code-reviewer` agent on the diff before marking DONE; fix
  P1/P2 findings first.
- Commit without AI attribution in messages or trailers (project rule).
