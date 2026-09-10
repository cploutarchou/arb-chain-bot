# Postgres backups and the restore drill

Production Postgres is a managed instance (`deploy/terraform/modules/postgres`):
provider automated backups with PITR (`backup_retention_days`), storage
encrypted with the environment KMS key. The manifests here add:

- `pgbackrest-configmap.yaml` / `backup-cronjob.yaml` — pgBackRest
  full/diff backups plus a 5-minute repo check into the `*-pg-backups`
  bucket (object-locked, KMS-encrypted). pgBackRest needs data-directory
  access on the database host, so these apply to a **self-hosted tier
  only**. On the managed tier do not apply them: a stanza with no
  backups keeps `BackupTooOld` firing, which is the intended signal
  rather than a silent, empty backup chain.
- `pgbackrest-externalsecret.yaml` — the backup role password only
  (`arb_backup`: REPLICATION + `pg_read_all_data`). No admin credential.
- `restore-drill-job.yaml` — weekly CronJob `pg-restore-drill`. Fails
  closed: it refuses to run unless `RESTORE_DRILL_TARGET_DISPOSABLE=1`,
  the target host is neither `pg1-host` nor `PRODUCTION_DB_HOST`, and a
  remote target carries a scratch marker (database `arb_drill*` /
  `arb_scratch*`, or an instance name containing `drill`/`scratch`).
  Two sources: `pgbackrest` (physical restore into the scratch instance
  inside the pod; self-hosted tier) and `verify-only` (read-only checks
  against a scratch instance the operator restored from a provider
  snapshot or PITR; managed tier). `verify.sql` runs under
  `default_transaction_read_only=on` after a write probe that must
  fail. Results: `pg_restore_drill_success`, `pg_restore_drill_refused`,
  `pg_restore_drill_duration_seconds`, `pg_restore_drill_timestamp` on
  the pushgateway plus the Job log; never a production table.

Runbook, including the managed-database backup options the operator has
to choose between: `docs/runbooks/restore-drill.md`.

Apply into the `arb` namespace after the chart; nothing here touches the
dev compose stack.
