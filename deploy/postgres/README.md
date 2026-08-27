# Postgres backups (pgBackRest) and restore drill

Managed Postgres already keeps a PITR window (terraform
`backup_retention_days`). These manifests add an independent,
provider-neutral copy: pgBackRest full/differential backups plus
continuous WAL archiving into the `*-pg-backups` object-storage bucket
(object-locked, KMS-encrypted). Two backup paths, one restore drill.

- `pgbackrest-configmap.yaml`  — repo + stanza config; credentials via
  workload identity or an ExternalSecret (`pgbackrest-externalsecret.yaml`).
- `backup-cronjob.yaml`        — full weekly (Sun 01:00 UTC), diff daily,
  WAL archive push every 5 min (`archive-push` for managed services that
  do not run pgBackRest inside; use `archive_command` when self-hosted).
- `restore-drill-job.yaml`     — restores the latest backup to a scratch
  Postgres, replays WAL to `--target`, runs the verification SQL and
  posts the result as a `restore_drills` row + Prometheus pushgateway
  metric `pg_restore_drill_success`. Scheduled weekly by
  `restore-drill-cronjob.yaml`; runbook: docs/runbooks/restore-drill.md.

Apply into the `arb` namespace after the chart; nothing here touches the
running dev compose stack.
