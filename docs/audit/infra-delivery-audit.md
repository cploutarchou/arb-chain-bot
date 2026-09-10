# Infrastructure, configuration and delivery audit

Audited tree: `master` at `abddb55` (2026-09-09). Scope: `Dockerfile`,
`deploy/docker`, `docker-compose.yml`, `deploy/helm`, `deploy/terraform`,
`deploy/observability`, `deploy/postgres`, `deploy/scripts`,
`.github/workflows`, `internal/config`, `internal/platform`, `cmd/*`,
`scripts/*`, `docs/deployment.md`, `docs/runbooks/*`. `helm` and
`terraform` are not installed in the audit environment, so chart rendering
and `terraform validate` were reviewed by hand; nothing was applied.

No P0: there is no path to live orders, no secret material in git, and no
data loss on a routine operation. Four P1s follow, all operational.

## Findings (ranked)

### I1 · P1 · The independent backup chain and the restore drill cannot work against the declared production database
- Evidence: production Postgres is a managed instance (`deploy/terraform/modules/postgres/example.tf:41-53`, `manage_master_user_password = true`); pgBackRest needs data-directory host access (`deploy/postgres/pgbackrest-configmap.yaml:27-31` `pg1-host=REPLACE_POSTGRES_ENDPOINT`, "informational for managed services"); the backup CronJob (`backup-cronjob.yaml:38,82,136`) and the drill (`restore-drill-job.yaml:37-38`) both use that stanza. `restore_drills` exists in no migration; `arb_backup` is created nowhere; the drill holds the production admin DSN (`pgbackrest-externalsecret.yaml:19-20`) and writes to production (`restore-drill-job.yaml:59-62`) while the runbook says production is never touched; the drill volume is 200 Gi against a 500 Gi instance; the pushgateway alerts (`deploy/observability/platform-rules.yml:104-126`) have no `absent()` counterpart, so a chain that never runs never alerts.
- Impact: the only working backup is provider PITR and it is never exercised; the RPO/RTO numbers in the runbook are unbacked.
- Action: restore the drill from a provider PITR/snapshot into a scratch instance and verify with `verify.sql`, or scope pgBackRest to a self-hosted tier; add `absent()` alerts, the `restore_drills` migration, a least-privilege drill credential, and size the drill volume from allocated storage.

### I2 · P1 · The bootstrap admin password, role and status are re-applied on every boot
- Evidence: `internal/app/components.go:1207-1232`, `internal/storage/authstore.go:58-66` (`ON CONFLICT (email) DO UPDATE SET password_hash, role, status`); `.env.example:47-54` and `docs/deployment.md:286` claim first-boot only; `scripts/create-secret.sh:17-20` copies the placeholder credentials into `.env`; the chart injects the password permanently (`templates/externalsecret.yaml:32-33`); no minimum length on the env value. (Same defect as `security-audit.md` S6.)
- Action: insert-only bootstrap (skip when `users` is non-empty), WARN when present but ignored, enforce `MinPasswordLength`, rotate the chart secret to empty after first login.

### I3 · P1 · The documented "fresh host" compose deployment publishes Postgres with a default password on all interfaces and a plaintext API
- Evidence: `docker-compose.yml:6` `POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-arb-dev-password}`, `:8-9` `"5432:5432"`, `:55-56,84-85` `"8080:8080"`; `docs/deployment.md:1-24,291-300` recommends this path for real recordings.
- Action: bind 5432 to loopback or drop the publication; `${POSTGRES_PASSWORD:?set in .env}` for the paper/record profiles; TLS-terminating proxy or loopback for 8080; state that compose is single-operator/dev only.
- Validation: `docker compose config` shows no `0.0.0.0` publication for 5432; the paper profile refuses to start without a password.

### I4 · P1 · The production canary stage cannot pass as written, and its design would run a second PAPER engine against the production database
- Evidence: `deploy/helm/canary-values.yaml:7,13-15` sets `mode: REPLAY` with an empty `ARB_REPLAY_SESSION` that `deploy.yml:197-202` never fills; `internal/config/config.go:145-147` refuses to start → CrashLoopBackOff → `--atomic` uninstall. Even with a session id, the stored platform document is authoritative (`internal/platform/service.go:133`, `internal/app/engine.go:437-443`), the canary shares `arb/prod/database-url`, so it would load mode PAPER and run a second live-feed engine from the same egress IP with the same outbox tables; the canary ingress sends 10 % of `/api` (including control POSTs) to it; `prev-rev.txt` is written and never read.
- Action: run the canary as the `cmd/api` profile (web + API, no engine) against read paths, or give the shadow engine its own database secret with `mode: REPLAY` and a session id passed via `--set`; assert in the chart that REPLAY requires a session id.

### I5 · P2 · The SLO bake gate fails open
- Evidence: `deploy/scripts/bake.sh:5,9-11` — POSIX `sh` with `set -eu` (no `pipefail`); a curl failure (empty `$am`, DNS, 5xx) yields an empty `firing` and "bake clean". `deploy.yml:206,240` pass the variable unvalidated.
- Action: bash with `set -euo pipefail`; fail if `$am` is empty or the first query is not 200; require one successful poll before declaring clean.

### I6 · P2 · Strategy (risk) parameters silently fall back to in-memory defaults when the stored version fails to load or validate
- Evidence: `internal/app/components.go:1078-1086` ("falling back to in-memory defaults"); contrast `:103-110`, where platform settings refuse to start on the same condition.
- Action: refuse to start when a database is configured; keep the memory path only for `ARB_DATABASE_URL` unset.

### I7 · P2 · Migrations run as a pre-upgrade hook against the live single-writer engine with no lock or statement timeouts
- Evidence: `deploy/helm/arb-platform/templates/migrate-job.yaml:13-18` (`pre-install,pre-upgrade`, 600 s deadline); `migrations/000009_…up.sql:19-28` warns about ACCESS EXCLUSIVE; no `statement_timeout`/`lock_timeout` anywhere; `internal/storage/store.go:22-39` sets only pool size and lifetime.
- Action: `lock_timeout` (5 s) and `statement_timeout` on the migrate DSN; `CREATE INDEX CONCURRENTLY` for populated tables; a scale-to-0 runbook step for lock-heavy migrations; pool-level `statement_timeout`.

### I8 · P2 · The ExternalSecret is a `before-hook-creation` hook with `creationPolicy: Owner`
- Evidence: `templates/externalsecret.yaml:14-16,24`. Every upgrade deletes the application Secret; a failed upgrade rolls back to a release whose Secret no longer exists; the canary uninstall orphans a pair.
- Action: make it a normal release resource with an init container that waits for the key, or `creationPolicy: Orphan` plus `hook-succeeded` delete policy.

### I9 · P2 · All scheduled jobs are in-process timers; the `worker`, `recorder` and `replay` binaries run nothing; there is no retention job
- Evidence: `internal/app/components.go:42-49,131,198,310,475` — only Full/API/Scanner profiles build components; `cmd/worker/main.go:20` produces an empty component list; `docs/architecture.md:29` says the worker runs reports, AI analyses and retention. Schedulers: `internal/reporting/reporting.go:394-427`, `internal/screener/report/scheduler.go:12-16` ("a missed slot is NOT back-filled"), `internal/ai/service.go:541-560`. No CronJob in the chart; no retention job (`internal/api/screenerapi.go:610-613` defers to one that does not exist); no partitioning; one `opportunities` row per qualified evaluation (`internal/app/engine.go:1143`).
- Action: implement the worker profile as CronJobs (`concurrencyPolicy: Forbid`) or delete the dead binaries and correct the docs; add a retention migration (partition `opportunities`/`risk_events` by month; entitlement-keyed deletes); persist "last successful run" per report kind for back-fill.

### I10 · P2 · Disk-full on the recordings volume is invisible to the recorder metrics and ends the session silently
- Evidence: `internal/marketdata/recorder.go:171-176` counts `written` on a successful `Append` into a 64 KiB buffer → zstd chain (`segment.go:101-105`); on ENOSPC the flush fails at rotation, `closeCurrent` returns before `OnSegment` (recorder.go:130-141); `openNext` failure ends `Run` (152-153,167-169) and `control.go:149-166` logs at Info while the engine continues; `recorder_frames_dropped_total` counts only queue overflow; the `RecorderNotWriting` rule keys on a kube-state-metrics label that is not exported by default.
- Action: count after flush/close success (`recorder_frames_persisted_total`, `recorder_write_errors_total`), export `recorder_session_error`, fsync on close, stop with a notification after N consecutive failures.

### I11 · P2 · Alerting blind spots and rule/metric mismatches
- Evidence: not exported at all — outbox drops/depth (`internal/storage/outbox.go:61-75` counters exist, none registered in `internal/metrics/metrics.go`), paper running/paused, supervisor `StateFailed`, pgx `EmptyAcquireCount`; `grafana-dashboard-platform.json:213,235` sums by `exchange` where the series label is `venue`; `platform-rules.yml:5-10` "PENDING EXPORTER" header is stale; no `absent()` guards; no postgres-exporter Deployment and no Alertmanager routing in the repo.
- Action: add `outbox_dropped_total`, `outbox_queue_depth`, `paper_running`, `engine_restart_state`, `db_pool_empty_acquires_total` and alerts `OutboxDropping`, `PaperEnginePaused`, `EngineRestartFailed`, `PersistenceDegraded`; fix the label; `promtool check rules` in CI plus a metric-name diff against a live scrape.

### I12 · P2 · Redis is provisioned in every environment, and nothing uses it
- Evidence: `deploy/terraform/envs/prod/main.tf:46-55`, `envs/paper-test/main.tf:48-55`, `values-prod.yaml:70`, `templates/networkpolicy.yaml:74-80`; no Redis client in `go.mod`; `docs/architecture.md:31,494` defers Redis until measured need.
- Action: remove the module and the egress rule until a consumer lands.

### I13 · P2 · CI/CD hardening gaps and a non-executable deploy pipeline
- Evidence: `ci.yml` — no `timeout-minutes`; actions pinned by major tag; `govulncheck@latest`; no helm lint/template, kubeconform, `terraform fmt/validate` (claimed in `deploy/terraform/README.md:22`), image build or scan on PR; E2E runs without Postgres. `deploy.yml:138-144,185-186,225-226` — cloud auth steps are `echo` placeholders, so smoke, bake and rollback have never executed; Trivy scans only the arbd image; `concurrency: deploy-${{ github.ref }}` lets two tags promote concurrently; `packages: write` + `id-token: write` on every job. Present and correct: gofmt, vet, build, `-race` tests with Postgres, golangci-lint (gosec on), govulncheck, frontend lint/typecheck/build, gitleaks, Playwright; no `continue-on-error`.
- Action: SHA-pin actions; pin govulncheck; timeouts; a `deploy-lint` job (helm lint/template with the LIVE negative guard, kubeconform, terraform fmt/validate `-backend=false`, hadolint); scan the web image; key concurrency on environment; wire real OIDC before the first tag.

### I14 · P3 · Log and trace shipping do not match what the code emits
- Evidence: `promtail-values.yaml:10-16` routes `{component="audit"}` but no log line carries it (audit goes to the table); Loki's 400-day audit retention retains nothing and the per-tenant override does not apply with `auth_enabled: false`; `tempo-values.yaml:1-4` is honest that no tracer exists; the NetworkPolicy allows egress only on 443 so the SMTP channel cannot connect.
- Action: emit audit lines with `component=audit` or document the table as the audit store; defer Tempo; add an SMTP egress rule or document the channel as unsupported in Kubernetes.

### I15 · P3 · Configuration and hardening residue
- Evidence: `ARB_ALLOWED_ORIGIN` defaults to `http://localhost:3000` and values-prod leaves it empty, so production settings v1 admit it as a WebSocket origin; `ARB_SHUTDOWN_GRACE` accepts 0/negative; `/healthz` is a constant 200 (`internal/api/server.go:270-273`); `arbd.replicas` unguarded (two engines on one DB and one RWO PVC); `deploy/docker/web.Dockerfile` claims standalone output that `web/next.config.ts` does not set and copies devDependencies; base images unpinned by digest; `.terraform.lock.hcl` only for prod; EKS node groups use the cluster role (placeholder); `observability/README.md:51-56` references a file that does not exist; PDB `maxUnavailable: 0` on a single replica blocks node drains.
- Action: set the prod origin and reject empty when ingress is enabled; validate grace > 0; fail liveness when the supervisor is `failed` past the grace; fail the template for `replicas > 1`; fix the web image; digest-pin; commit the paper-test lock file; correct the README.

## Verified correct

- No path to live execution: `LiveExecutor` unconditional refusal; no build tags; `config.Mode.Valid()` has no LIVE value; `platform.ValidateMode` refuses LIVE by name; the chart helper fails the render on anything but RECORD/PAPER/REPLAY (`templates/_helpers.tpl:61-68`), `deploy.yml:115-117` asserts that failure, `smoke.sh:15-16` re-checks the rendered ConfigMap; all values files pin `mode: PAPER`; a bare `docker compose up` starts only `db` and `migrate`.
- Config fails fast where it matters: invalid mode, grace, seed, allow-list entries, REPLAY/BACKTEST without a session (`internal/config/config.go:115-147`); platform settings validated at boot and apply (fees in (0,100], token discount refused, balances bounded, mode/log level/origin, AI budget); stored active version re-validated on every boot and a load failure refuses to start; unreachable database refuses to start; strategy `Params.Validate` bounds every field; secrets redacted by key and `Bootstrap.Redacted` enforced by test.
- Environment separation and secret handling: no secret material in values, templates, Terraform or workflows; credentials reach pods only via ExternalSecret; Terraform creates empty secret shells, manages the master password, `prevent_destroy` on DB and buckets, KMS, public-access block, versioning, COMPLIANCE object lock on backups; encrypted S3 state with a lock table.
- Runtime hardening: multi-stage static build, alpine, unprivileged user, HEALTHCHECK; pod security context matches the image UID/GID, read-only root, `drop ALL`, `RuntimeDefault` seccomp, no service-account token; probes hit real endpoints (`/readyz` pings the pool); requests/limits everywhere; PDBs; HPA only on the stateless web tier; default-deny NetworkPolicy with metadata exclusion; recordings PVC RWO with `resource-policy: keep` and `Recreate`; migration hook ordering correct; `terminationGracePeriodSeconds` exceeds the shutdown grace plus outbox drain.
- Supply chain in `deploy.yml`: build once, promote by digest; BuildKit provenance + SBOM; keyless signing; Trivy CRITICAL gate on arbd; ephemeral registry token; `package.sh` copies the exact commit's migrations and lints; CI permissions minimal.
- Rules and dashboards reference real series with matching labels; histogram units match thresholds; the ServiceMonitor injects the `environment` label; alerts exist for feed stale/silent, book unhealthy, breaker open, paper failure rate, recorder drops, collector 429s, migrations, replication lag, PVC filling, CPU throttling, multi-window SLO burn.
- Overlap protection where it exists: report generation serialised; campaign/replay runs carry owner/heartbeat with stale reclaim (`internal/jobrun`, migration 000009); the supervisor tags engine generations and refuses overlapping restarts.
- Failure-injection status: DB unavailable at boot → fail fast (handled); DB unavailable mid-run → `readyz` 503, outbox logs + `OnPersistError` hook, pgx reconnects (handled but unobservable in Prometheus, I11); Redis absent → unused; restart with in-flight cycles → paper engine waits for goroutines and the supervisor pauses first, but results enqueued after the 3 s drain are lost and in-memory balances reset (partially handled, documented); disk full → unhandled (I10); clock skew → unhandled (`market-data-audit.md` M2).
