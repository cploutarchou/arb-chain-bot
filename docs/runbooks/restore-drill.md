# Runbook: Postgres restore drill

Owner: platform on-call. Cadence: weekly (CronJob `pg-restore-drill`,
Mondays 04:30 UTC) and on demand before any migration that touches
`paper_cycles`, `orders`, `fills`, `secrets`, or `campaign_runs`.

A backup that has not been restored is not a backup. The drill restores
a backup into a throwaway database, runs `verify.sql` read-only, and
reports `pg_restore_drill_success`, `pg_restore_drill_refused`,
`pg_restore_drill_duration_seconds` and `pg_restore_drill_timestamp` to
the pushgateway (alerts `RestoreDrillFailed`, `RestoreDrillOverdue`).
The Job log is the record of each run. There is no `restore_drills`
table: none exists in the migrations, and a row written from the drill
would have been a write into production from a job that must never
write there.

## What the drill refuses (fail closed)

`drill.sh` (ConfigMap `pg-restore-drill-scripts`) exits 2, pushes
`pg_restore_drill_refused 1` and `pg_restore_drill_success 0`, and does
nothing else when any of these holds:

| Guard | Rule |
|---|---|
| Disposable flag | `RESTORE_DRILL_TARGET_DISPOSABLE` must be `1`. The CronJob sets it because its own target is the scratch instance inside the pod, on an ephemeral volume that is deleted with the pod. |
| Production endpoint | The target host must equal neither `pg1-host` from `pgbackrest.conf` nor `PRODUCTION_DB_HOST` (both the managed endpoint from the terraform output). |
| Scratch marker | A target that is not loopback must name a database `arb_drill*` / `arb_scratch*`, or an instance whose first host label contains `drill` or `scratch`. `.../arb` on an unmarked remote host is refused. |
| Explicit database | The DSN must name a database. |
| Source and target fit | `RESTORE_DRILL_SOURCE=pgbackrest` restores into the in-pod instance only; a remote scratch instance is checked with `verify-only`. |

Verification runs with `default_transaction_read_only=on`; the drill
first proves that guard holds by attempting a `CREATE TABLE`, which has
to fail. The pod holds the backup role password (`arb_backup`,
`pg_read_all_data`) and no admin credential. An unexpected exit still
reports `pg_restore_drill_success 0` (EXIT trap), so a broken drill is
never silent.

## Targets

| Objective | Target | Measured by |
|---|---|---|
| RPO | <= 5 min | provider PITR window (`backup_retention_days`, continuous WAL upload) on the managed tier; `pg_backup_last_success_timestamp` on a self-hosted tier |
| RTO | <= 60 min for a full prod database | `pg_restore_drill_duration_seconds` of a verify-only drill plus the provider restore time, or of a physical restore on a same-size volume |
| Drill freshness | <= 8 days | alert `RestoreDrillOverdue` |

Nothing here is guaranteed; these are the objectives the drill measures
against, and a failing or refused drill is a critical alert. Until a
drill has run against the managed tier (below), the RPO/RTO figures are
targets, not measurements.

## Two sources, one class of target

The production database (`deploy/terraform/envs/prod`) is a managed
instance. pgBackRest needs host access to the data directory, so the
physical path applies to a self-hosted tier only.

| `RESTORE_DRILL_SOURCE` | What happens | Applies to |
|---|---|---|
| `pgbackrest` (CronJob default) | `pgbackrest restore --type=time` into `/pgdata`, start Postgres on `127.0.0.1:5433` with a loopback-only `pg_hba.conf`, wait for promotion, verify. | self-hosted Postgres with a working stanza |
| `verify-only` | No restore. Verify against `RESTORE_DRILL_TARGET_DSN`, a scratch instance the operator restored from a provider snapshot or PITR. | the managed production database |

### Run the physical drill on demand (self-hosted tier)

```sh
kubectl -n arb create job --from=cronjob/pg-restore-drill drill-$(date +%s)
kubectl -n arb logs -f job/drill-<ts>
```

Point-in-time target (UTC): set `RESTORE_TARGET=2026-08-27T03:00:00Z`
on the Job before creating it (`kubectl create job ... --dry-run=client
-o yaml`, edit, `kubectl apply -f -`).

### Run the drill against the managed database (verify-only)

1. Restore from the provider into a NEW instance whose name carries the
   scratch marker, e.g. `arb-prod-drill-20260909` (RDS: restore to point
   in time or restore from snapshot; same subnet group and security
   group as the primary, not publicly accessible, deletion protection
   off, skip the final snapshot). Terraform is not involved: the
   instance is throwaway and is deleted in step 4.
2. Create the Job from the CronJob with the source and target overridden:

```sh
kubectl -n arb create job --from=cronjob/pg-restore-drill drill-$(date +%s) --dry-run=client -o yaml \
  | yq '(.spec.template.spec.containers[0].env[] | select(.name == "RESTORE_DRILL_SOURCE")).value = "verify-only"
      | (.spec.template.spec.containers[0].env[] | select(.name == "RESTORE_DRILL_TARGET_DSN")).value
        = "postgres://arb_backup@arb-prod-drill-20260909.<region>.rds.amazonaws.com:5432/arb?sslmode=require"' \
  | kubectl apply -f -
```

   The password comes from `PGPASSWORD` in `pgbackrest-credentials`
   (the read-only backup role); the DSN carries none. The host label
   `arb-prod-drill-...` satisfies the scratch marker, so the database
   can stay `arb`. Pointing the DSN at the production endpoint is
   refused by the endpoint guard even if the flag is set.
3. Read the Job log. RTO for the record is the provider restore time
   plus `pg_restore_drill_duration_seconds`.
4. Delete the scratch instance.

Expected log tail (either source):

```
restore drill: source=... target=... point_in_time=...
 migration_version | <n>
 markets | <n>  campaign_runs | <n>  recordings | <n>
 float_money_columns | 0
 vault_rows | <n>
restore drill: success=1 duration=<s>s source=... target=...
```

## Managed-database backup path: a decision for the operator

What exists today: provider automated backups with PITR
(`backup_retention_days = 14` in prod, 7 in paper-test), storage
encrypted with the environment KMS key, a final snapshot on deletion,
`prevent_destroy` on the instance. The pgBackRest CronJobs in
`deploy/postgres` cannot back up a managed instance (`pg1-host` has no
data-directory access), so on the managed tier they are not applied; if
they are, `BackupTooOld` fires and stays firing, which is the intended
signal.

Choose one of the following and record the choice in
`docs/decisions/`. The drill supports each; none is implemented here
beyond what the table says.

| Option | Adds | RPO and independence | Cost and caveats |
|---|---|---|---|
| A. Provider PITR and snapshots only | Nothing new. Schedule a weekly `verify-only` drill against a PITR restore (above) and a periodic cross-region snapshot copy. | RPO from the provider's continuous WAL upload (about 5 min on RDS). Copies live in the same provider account. | Lowest effort. Account-level loss or a provider outage takes the backups with the database. |
| B. A plus a logical dump schedule | A CronJob running `pg_dump -Fc` from the read replica as `arb_backup`, written to the `*-pg-backups` bucket (object lock, KMS) under the existing retention. The drill restores the newest archive into the in-pod scratch instance as `arb_drill` (the `pgbackrest` source with `pg_restore` instead of a stanza; a small script change). | RPO of the logical copy = dump interval (daily suggested). The copy is provider-neutral and restorable anywhere `pg_restore` runs. | Replica load during the dump and bucket storage (compressed dump, roughly 10-20 % of data size). Not a replacement for PITR: point-in-time granularity stays with A. Needs no provider-specific code. |
| C. Self-hosted tier with pgBackRest | The manifests as written (`backup-cronjob.yaml`, stanza with a real `pg1-host`/`pg1-path`): physical full/diff plus WAL archiving to the bucket. | RPO about 5 min from WAL push; fully independent of the provider. | Only if Postgres moves off the managed service (Patroni or similar). Not applicable to the current terraform. |

Not implemented here on purpose: provider-specific snapshot automation
and the option B CronJob. Until a choice is recorded, the weekly
CronJob's physical drill fails at the pgBackRest restore step on the
managed tier (no usable stanza) and reports `RestoreDrillFailed`, which
is the honest state; pointing it at production is refused regardless.
The open decision is framed in
`docs/decisions/2026-09-10-backup-automation-open-operator-decision.md`
(P1-15) — record the chosen option there when it is made.

## verify.sql checks (read-only)

1. `schema_migrations` has the expected version and `dirty=false`.
2. Core tables readable with plausible counts.
3. No money column is `real`/`double precision` (decimal money math is
   a data-integrity rule, so the drill enforces it).
4. `secrets` rows restored as ciphertext. The drill pod has no
   `ARB_SECRET_KEY`; it cannot and must not decrypt exchange keys.
5. Newest `paper_cycles.started_at` lies inside the restore target.

## When the drill fails or is refused

1. Refused (`pg_restore_drill_refused 1`, exit 2): the log names the
   guard. Fix the target, never the guard.
2. Restore failed (pgBackRest error; self-hosted tier) ->
   `pgbackrest --stanza=arb info` from a `pg-backup-check` pod; check
   bucket IAM (workload identity role) and object-lock state. Run a
   manual `--type=full` backup once fixed.
3. Postgres did not reach `pg_is_in_recovery()=f` within 10 min ->
   WAL gap. Check WAL archiving and the `archive-async` spool. The RPO
   is breached for the missing window; open an incident.
4. `verify.sql` failure -> a migration or data rule broke. Do not
   retry blindly: page the backend owner, attach the log to the
   incident, and block the next deploy (the `MigrateJobFailed` /
   `RestoreDrillFailed` alerts hold the bake gate in deploy.yml).
5. "read-only guard did not hold" -> the target accepted a write, which
   means the session options were dropped (a pooler in transaction mode
   ignores `PGOPTIONS`). Connect to the instance directly.

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
   the primary in place. Run a `verify-only` drill against the new
   instance before repointing (its name must carry the scratch marker
   for the drill to accept it; rename or use `arb_drill` created with
   `CREATE DATABASE arb_drill TEMPLATE arb` if it does not).
4. pgBackRest path (self-hosted tier only): run the drill Job with
   `RESTORE_TARGET=<time>` on a 1:1 sized volume, then
   `pg_dump | psql` into the new instance, or promote the drill volume
   as an interim primary.
5. Repoint: update the `arb/prod/database-url` secret in the KMS
   store; External Secrets refreshes within `refreshInterval` (force
   with `kubectl annotate externalsecret ... force-sync=$(date +%s)`).
6. Re-run migrations by upgrading the release to the same revision
   (`helm upgrade --reuse-values`); the hook is idempotent.
7. Scale arbd back to 1. Smoke: `deploy/scripts/smoke.sh arb arb`.
8. Post-incident: file the RCA with the Job log attached, and re-run
   the drill against the new instance within 24 h.

## Rollback of a bad migration (no data loss yet)

Down migrations are never run by the hook. From a
`migrate/migrate:v4.18.3` pod with the migrations ConfigMap mounted:

```sh
migrate -path /migrations -database "$ARB_DATABASE_URL" down 1
```

Only after `helm rollback` to the previous chart revision (older
binary must not see the newer schema first or vice versa — check the
migration's compatibility note in its header).
