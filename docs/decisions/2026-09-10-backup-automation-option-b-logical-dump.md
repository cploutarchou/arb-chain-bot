# Decision: backup automation — option B, a provider-neutral logical dump beside provider PITR (P1-15)

- Date: 2026-09-10 (supersedes the OPEN record of the same date)
- Decided by (operator, legal name and role): the repository operator (product
  owner for infrastructure), acting on the remediation instruction "do all
  blocking". Legal name not recorded in the material this write-up was made
  from; the operator fills it in on signing.
- Counsel consulted (firm, person, date): none — not applicable. Infrastructure
  redundancy choice; no regulated activity is created or narrowed.
- Jurisdictions considered: the backups bucket region (eu-central-1 per the
  pgBackRest config) holds the only provider-neutral copy; EU data-residency
  is preserved because the bucket is the same one the physical repo uses.
- Licence / exemption relied on (per jurisdiction): unchanged — signals-only
  SaaS, `docs/design/packages.md` §7.
- Scope: own funds. Platform data only; no client execution exists.
- Evidence reviewed (docs/campaigns/ report ids, dates): not applicable — no
  profitability claim is involved or made here.
- Security review reference: `docs/audit/security-audit.md` I1 (the finding
  this closes), S-series for the credential shape below.
- Insurance in place: not applicable.
- Decision: **Option B of `docs/runbooks/restore-drill.md` — provider PITR
  and snapshots (option A) remain the base, plus a daily `pg_dump -Fc` from
  the backup role (`arb_backup`: REPLICATION + `pg_read_all_data`, no admin
  credential) into the same object-locked, KMS-encrypted `*-pg-backups`
  bucket (`deploy/postgres/pgdump-cronjob.yaml`, image
  `deploy/docker/pg-dump.Dockerfile`). The restore drill gains a `pgdump`
  source that restores the newest archive into its in-pod scratch instance
  with `pg_restore` (`deploy/postgres/restore-drill-job.yaml`); the drill
  talks to no cloud service itself, so it holds no bucket credentials.**
- Evidence the drill passes: exercised 2026-09-10 against a local
  PostgreSQL 16 (migrations 000001–000021): `pg_dump -Fc` → drill with
  `RESTORE_DRILL_SOURCE=pgdump` → guards accepted the loopback scratch
  target, the archive restored, the read-only write-probe failed as
  required, and all five verify checks passed
  (`restore drill: success=1 duration=1s source=pgdump target=loopback`);
  without `RESTORE_DRILL_TARGET_DISPOSABLE=1` the drill REFUSES. The same
  exercise found and fixed a real defect: verify.sql's `CASE … 1/0` checks
  errored on every run because PostgreSQL constant-folds constant division
  in a not-provably-dead CASE arm; they are now DO/RAISE blocks.
- Conditions and expiry: the weekly CronJob's drill must run with
  `RESTORE_DRILL_SOURCE=pgbackrest` on the self-hosted tier and
  `pgdump` (or `verify-only` against a provider PITR restore) on the
  managed tier, at least monthly, until two consecutive drills pass in the
  production namespace; `pg_dump_last_success_timestamp` older than 36 h
  fires `DumpTooOld` (deploy/observability/platform-rules.yml). The
  logical copy's RPO is 24 h (the 02:15 UTC schedule); point-in-time
  recovery stays with the provider's WAL upload. Revisit if the database
  grows past what a daily replica dump can absorb.
- Supersedes: `docs/decisions/` record of 2026-09-10 framing this as OPEN
  (same file, rewritten).
