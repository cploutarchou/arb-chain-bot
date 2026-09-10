# Decision: managed-database backup automation stays an open operator decision (P1-15)

- Date: 2026-09-10
- Decided by (operator, legal name and role): NOT DECIDED — this record frames the
  open decision the audit left with the operator (P1-15); the operator fills in the
  choice and date when they make it. The engineering half that could be decided
  without the operator (the restore drill fails closed and never writes to
  production) is evidenced by commit `5357976` and this record.
- Counsel consulted (firm, person, date): none — not applicable. Infrastructure
  redundancy choice; no regulated activity is created or narrowed.
- Jurisdictions considered: all markets equally; data-residency implications of the
  cross-region copy in option A and the bucket region in option B are part of what
  the operator must weigh.
- Licence / exemption relied on (per jurisdiction): unchanged — signals-only SaaS,
  `docs/design/packages.md` §7.
- Scope: own funds. Platform data only; no client execution exists.
- Evidence reviewed (docs/campaigns/ report ids, dates): not applicable — no
  profitability claim is involved or made here.
- Security review reference: `docs/audit/security-audit.md` S-series and
  `docs/audit/infra-delivery-audit.md` I1 (the finding this answers), reviewed on the
  audit branch.
- Insurance in place: not applicable.
- Decision: **OPEN — the operator chooses one of the three options in
  `docs/runbooks/restore-drill.md` §"Managed-database backup path" (A provider
  PITR/snapshots only; B plus a logical dump schedule to the object-storage bucket;
  C self-hosted pgBackRest) and records it here.** What is already fixed and is not
  part of the choice: the drill refuses a non-disposable target, never writes to the
  production database, restores into an in-pod scratch instance under a read-only
  session, and reports `RestoreDrillFailed` honestly while no option is chosen —
  the weekly physical drill on the managed tier fails at the pgBackRest step because
  no usable stanza exists, which is the intended signal, not a defect.
- Conditions and expiry: the weekly `verify-only` drill must be scheduled within 30
  days of the operator's choice; until a choice is recorded the RPO claim in any
  status page or marketing material is "provider default (unverified by a drill)".
- Supersedes: nothing. First record for this decision area; P1-15's engineering half
  was closed without one because it changed no backup path, only refusal semantics.
