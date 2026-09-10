# Database and storage audit

Audited tree: `master` at `abddb55` (2026-09-09). Scope: `docs/data-flow.md`
§4–§6, `docs/architecture.md` §12, all 15 migrations (up and down),
`internal/storage/*.go`, outbox and hot-path call sites in `internal/app`,
screener storage/service/API wiring, tenancy migration and RBAC,
`cmd/worker`, `deploy/postgres/*`, `docs/runbooks/restore-drill.md`.
`go build ./...`, `go vet ./...` clean; `go test ./internal/storage/...`
passes (DB-backed tests skip cleanly without `ARB_TEST_DATABASE_URL`).

## Findings (ranked)

### D1 · P1 (P0 once a second organisation exists) · Any tenant's org admin can overwrite the platform-wide screener configuration
- Evidence: `migrations/000013_tenancy.up.sql:65-78` adds `org_id` to six screener tables but not `screener_settings`, which keeps its 000010 shape including the platform-wide `UNIQUE … WHERE active` index (`000010_screener.up.sql:22`). `internal/storage/screener.go:65-74,110-116` query it with no org predicate; `internal/screener/service.go:236-243,265-330` hold one process-wide `atomic.Pointer` snapshot; `internal/app/components.go:1185` builds one `screener.Service`. `POST /api/v1/screener/settings` is gated by `PermScreenerConfig` (console ADMIN, `internal/auth/rbac.go:34-39`), not `platform_admin`; `TestSystemRoutesArePlatformAdminOnly` (`internal/api/tenancy_test.go:178-197`) does not list it, and `tenancy_test.go:319-324` has a plain tenant owner post it successfully.
- Impact: any organisation ADMIN/OWNER can change poll interval, venues/tiers, paper balances and thresholds for the entire platform; per-tenant screener behaviour is impossible despite migration 000013 being built for it. Today every account lands in the platform organisation (`security-audit.md` S4), so the practical blast radius is "every user", which is the same population.
- Action: add `org_id` to `screener_settings` (`UNIQUE(org_id) WHERE active`), thread `orgFilter` through `Active/Get/List/Insert` and key the service snapshot by org; require `platform_admin` for any field that stays global.
- Validation: two organisations; org A changes `poll_interval_s`; org B's `GET /api/v1/screener/settings` is unchanged.

### D2 · P1 (P0 once a second organisation exists) · Nightly screener reports commingle and expose every tenant's paper-trading data
- Evidence: `migrations/000012_screener_reports.up.sql:12-21` has no `org_id`; `internal/storage/screener_reports.go:34-60` cannot filter; the generator reads with a background context (`internal/screener/report/generator.go:99,103` → `ListPositions`/`ListExecutions` with no `tenancy.WithOrg`), so `orgFilter` (`internal/storage/screener.go:24-29`) applies no predicate and the report spans every organisation's positions and executions; `GET /api/v1/screener/reports[/{id}]` is `PermScreenerView`, held by VIEWER.
- Impact: a read-only VIEWER at any organisation reads strategy performance computed over every other tenant's paper activity; the report's own meaning is diluted.
- Action: add `org_id`; generate one report set per organisation (plus a clearly separated platform aggregate); scope the generator's reads per org.

### D3 · P1 · Outbox silently drops financial records with no working persistence breaker
- Evidence: `internal/storage/outbox.go:23-24` declares `OnPersistError`; no assignment exists (the only constructor is `internal/app/engine.go:726`). `Enqueue` (`outbox.go:49-58`) returns false on overflow and every caller ignores it (`engine.go:841,974,1143,1165`). `write()` (`outbox.go:112-137`) does one INSERT per record despite `docs/architecture.md:324` ("COPY/multi-row"); `engine.go:186` concedes it. `Dropped()`/`Written()` are read only by the system-health map (`internal/app/readmodel.go:165-169`), not exported as metrics, and no alert rule mentions the outbox.
- Impact: under DB slowness or a burst that saturates the 4096-slot channel, qualified opportunities, settled cycles (with orders/fills) and risk events are dropped with only an in-memory counter. (The shutdown race that loses the last cycles is `execution-risk-audit.md` F7.)
- Action: wire `OnPersistError` and a drop-rate watermark to a breaker that raises a CRITICAL alert; write a `system_events` row recording the loss; export `outbox_dropped_total`/`outbox_written_total`/`outbox_queue_depth`; alert rule.
- Validation: unreachable DSN, drive cycles faster than `QueueSize`, assert alert + metric + loss record.

### D4 · P1 · Portfolio and PnL history tables are dead schema
- Evidence: `migrations/000001_initial.up.sql:177-207` define `virtual_balances`, `balance_snapshots`, `pnl_snapshots`; no Go code references any of them; `internal/portfolio/portfolio.go:105-130` computes snapshots in memory only; `internal/api/reads.go:128-143` serves `/portfolio` and `/pnl` from the engine, with no history route.
- Impact: balance/PnL history including drawdown does not survive a restart; `docs/architecture.md` §15 "paper session resumes from persisted state" is unimplemented (see `execution-risk-audit.md` F8).
- Action: implement the documented snapshot writer through the outbox, or drop the tables and correct the docs.

### D5 · P1 · No retention enforcement exists, and `cmd/worker` builds no components
- Evidence: `docs/data-flow.md:169` and `docs/architecture.md:29` assign retention to `cmd/worker`; `internal/app/components.go:48` declares `ProfileWorker` and never references it again — schedulers are gated on Full/API (`:198,242`) and Full/Scanner (`:310-374`); `cmd/worker/main.go` therefore starts nothing. No `DELETE` of `opportunities`, `exchange_health`, `system_events`, `risk_events` or `paper_cycles` exists in `internal/storage`.
- Impact: `opportunities` (qualified and sampled rejections, written continuously), `exchange_health`, `system_events`, `risk_events` grow forever; the documented 90 d / 14 d / 30 d policy is unimplemented.
- Action: a `ProfileWorker` branch wiring the schedulers plus a retention component running bounded batched deletes with an audited `system_events` row per pass.

### D6 · P1 · Insert-only guarantee on `audit_events`/`risk_events` is convention only
- Evidence: `migrations/000001_initial.up.sql:1-4` defers grants "per environment"; no `GRANT`/`REVOKE`/`CREATE ROLE`/trigger anywhere in `migrations/`, `deploy/`, `scripts/`; compose runs migrations and the app as one role; `internal/storage/configs_test.go:23-28` deletes from `audit_events` as cleanup.
- Action: a migration creating a low-privilege application role (or `REVOKE UPDATE, DELETE` on both tables) and a separate migration-runner role; or state the single-role model explicitly in the docs.

### D7 · P2 · Missing indexes for two console queries
- Evidence: `internal/storage/lists.go:75-81` (`ListCycles`, `WHERE session_id = $1 ORDER BY started_at DESC`) has no `(session_id, started_at DESC)` index (existing: `(session_id, settled_at DESC)`, global `(started_at DESC)`, `(opportunity_id, started_at DESC)`); `internal/storage/orders_fills.go:114-133,200-219` filter on joined `m.symbol`, `op.triangle_id`, `o.status` with only the `(created_at DESC, id DESC)` / `(ts DESC, id DESC)` indexes.
- Action: `paper_cycles (session_id, started_at DESC)`; `markets (symbol)`; consider materialising symbol/triangle on `orders`; create `CONCURRENTLY` once tables are populated.

### D8 · P2 · Single shared small pool with no statement or HTTP timeouts
- Evidence: `internal/storage/store.go:27-28` (`MaxConns = 8`, `MaxConnLifetime` only); no `statement_timeout` anywhere; `internal/api/server.go:245-248` sets only `ReadHeaderTimeout`; the outbox writer and the API share the pool (`internal/app/components.go:313,512`).
- Impact: a few slow API queries (D9) can exhaust the pool and stall persistence of cycles from the hot path, feeding D3.
- Action: `statement_timeout` via `AfterConnect`/DSN; HTTP read/write/idle timeouts; reserve outbox capacity (separate pool or `MinConns`).

### D9 · P2 · Unbounded query on `GET /api/v1/screener/funding`
- Evidence: `internal/api/screenerapi.go:316-327` accepts any positive `hours`; `internal/storage/screener.go:408-423` has no `LIMIT` in any branch.
- Action: clamp `hours` to the entitlement's retention ceiling; add `LIMIT`/pagination.

### D10 · P2 · No CHECK constraints on NUMERIC financial columns
- Evidence: `grep -n CHECK migrations/*.up.sql` matches only TEXT enum columns; `virtual_balances.available/reserved`, `orders.qty_*`, `fills.price/qty`, fee columns are unconstrained.
- Action: `>= 0` checks where negativity is never valid; leave PnL/slippage columns unconstrained.

### D11 · P3 · The restore drill's audit insert targets a table that does not exist; the runbook example is stale
- Evidence: `deploy/postgres/restore-drill-job.yaml` inserts into `restore_drills` (no migration creates it; the fallback `echo` swallows it); `docs/runbooks/restore-drill.md:39` shows `migration_version | 10` while `internal/storage/migrations.go:18` is 15.
- Action: a migration for `restore_drills`; regenerate the runbook example from `LatestMigrationVersion`.

## Verified correct

- Every financial column across all 15 migrations is `NUMERIC`; no `real`/`double precision`; all timestamps `TIMESTAMPTZ`; ULID text keys; the restore drill's `verify.sql` re-checks the float rule.
- `InsertCycle` (`internal/storage/records.go:156-233`) writes cycle, reference rows, orders and fills in one `pgx.Tx` with `ON CONFLICT (id) DO NOTHING` throughout.
- Keyset cursor pagination on orders/fills with tuple comparison and RFC3339Nano encoding, backed by matching composite DESC indexes (`migrations/000007…up.sql:33-34`).
- Job ownership: `owner_id`/`heartbeat_at` with partial indexes on active statuses (migration 000009, `internal/jobrun`).
- Tenant scoping is correct on `screener_rules/templates/events/paper_*` via `orgFilter`; platform-only routes (`/users`, `/platform/settings`, `/orgs`, `/billing/prices`) and the exchange-credential vault are `platform_admin`-gated with passing tests; market-structure tables are correctly unscoped.
- Hot data stays out of Postgres: screener quotes are in-memory (`internal/screener/book.go`); `funding_history` is deduped on the venue's funding timestamp (`internal/screener/venue/poller.go:263`), so growth tracks funding events, not polls.
- Backup design (weekly full / daily diff / continuous WAL, weekly restore drill with a meaningful verification script) is sound in shape; see `infra-delivery-audit.md` I1 for why it cannot run against the declared managed instance.
