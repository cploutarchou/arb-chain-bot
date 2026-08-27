# Runbook: Postgres restore drill

Owner: platform on-call. Cadence: weekly (CronJob `pg-restore-drill`,
Mondays 04:30 UTC) and on demand before any migration that touches
`paper_cycles`, `orders`, `fills`, `secrets`, or `campaign_runs`.

A backup that has not been restored is not a backup. The drill restores
the latest pgBackRest backup into a scratch Postgres inside the Job pod,
replays WAL to a point in time, runs `verify.sql`, and reports
`pg_restore_drill_success` / `pg_restore_drill_duration_seconds` to
Prometheus plus a row in `restore_drills`. Production is never touched.

## Targets

| Objective | Target | Measured by |
|---|---|---|
| RPO | <= 5 min (WAL archive) | `pg-backup-check` -> `pg_backup_last_success_timestamp`, provider PITR window |
| RTO | <= 60 min for full prod DB | `pg_restore_drill_duration_seconds` (drill on a same-size volume) |
| Drill freshness | <= 8 days | alert `RestoreDrillOverdue` |

Nothing here is guaranteed; these are the objectives the drill measures
against, and a failing drill is a critical alert.

## Run the drill on demand

```sh
kubectl -n arb create job --from=cronjob/pg-restore-drill drill-$(date +%s)
kubectl -n arb logs -f job/drill-<ts>
```

Optional point-in-time target (UTC): edit the Job env `RESTORE_TARGET`
(`2026-08-27T03:00:00Z`) before creating it, or
`kubectl create job ... --dry-run=client -o yaml | yq ... | kubectl apply -f -`.

Expected log tail:

```
restore drill: stanza=arb target=...
 migration_version | 10
 markets | <n>  campaign_runs | <n>  recordings | <n>
 float_money_columns | 0
 vault_rows | <n>
restore drill: success=1 duration=<s>s
```

## verify.sql checks

1. `schema_migrations` has the expected version and `dirty=false`.
2. Core tables readable with plausible counts.
3. No money column is `real`/`double precision` (decimal money math is
   a data-integrity rule, so the drill enforces it).
4. `secrets` rows restored as ciphertext. The drill pod has no
   `ARB_SECRET_KEY`; it cannot and must not decrypt exchange keys.
5. Newest `paper_cycles.started_at` lies inside the restore target.

## When the drill fails

1. Read the Job log. Distinguish: restore failed (pgBackRest error),
   Postgres did not come up, or `verify.sql` failed.
2. pgBackRest error -> `pgbackrest --stanza=arb info` from a
   `pg-backup-check` pod; check bucket IAM (workload identity role) and
   object-lock state. Run a manual `--type=full` backup once fixed.
3. Postgres did not reach `pg_is_in_recovery()=f` within 10 min ->
   WAL gap. Check provider WAL archiving and `archive-async` spool.
   The RPO is breached for the missing window; open an incident.
4. `verify.sql` failure -> a migration or data rule broke. Do not
   retry blindly: page the backend owner, attach the log to the
   incident, and block the next deploy (the `MigrateJobFailed` /
   `RestoreDrillFailed` alerts hold the bake gate in deploy.yml).

## Real restore (incident)

This is the only path that touches production data. Two people, in
a call, with the incident channel open.

1. Freeze writes: `kubectl -n arb scale deploy/arb-arb-platform-arbd --replicas=0`
   (the PDB does not block a voluntary scale-down). Note the exact UTC
   time; recordings on the PVC are unaffected.
2. Pick the target: last known-good time from the incident timeline,
   never "now".
3. Managed-service path (preferred, faster): provider PITR to a NEW
   instance from the primary's automated backups; keep the old
   instance until sign-off. Terraform: add the restored instance as a
   new `aws_db_instance` with `restore_to_point_in_time`; never modify
   the primary in place.
4. pgBackRest path (provider backups unavailable): run the drill Job
   with `RESTORE_TARGET=<time>` and a 1:1 sized volume, then
   `pg_dump | psql` into the new managed instance, or promote the
   drill volume as an interim self-hosted primary.
5. Repoint: update the `arb/prod/database-url` secret in the KMS
   store; External Secrets refreshes within `refreshInterval` (force
   with `kubectl annotate externalsecret ... force-sync=$(date +%s)`).
6. Re-run migrations by upgrading the release to the same revision
   (`helm upgrade --reuse-values`); the hook is idempotent.
7. Scale arbd back to 1. Smoke: `deploy/scripts/smoke.sh arb arb`.
8. Post-incident: record the drill row manually if the Job did not,
   file the RCA, and re-run the drill against the new instance within
   24 h.

## Rollback of a bad migration (no data loss yet)

Down migrations are never run by the hook. From a
`migrate/migrate:v4.18.3` pod with the migrations ConfigMap mounted:

```sh
migrate -path /migrations -database "$ARB_DATABASE_URL" down 1
```

Only after `helm rollback` to the previous chart revision (older
binary must not see the newer schema first or vice versa — check the
migration's compatibility note in its header).
