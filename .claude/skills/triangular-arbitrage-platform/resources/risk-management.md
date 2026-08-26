# Resource: Risk Management

Authoritative document: `docs/risk.md`.
Implementation home: `internal/risk`, `internal/reservation`.

Working rules:
- Risk engine is deterministic: pure function of (opportunity, RiskContext
  snapshot, config version). AI never overrides; web/Telegram change
  limits only through RBAC-gated, audited, versioned config.
- Two gates: qualification (hot path) and execution (reservation +
  revalidation). Both cite reason codes; rejections are observable.
- Limits per docs/risk.md §2 with global → exchange → starting-asset →
  triangle override precedence (most specific wins; effective values are
  recorded in each decision).
- Circuit breakers per docs/risk.md §3: CLOSED/OPEN/HALF_OPEN; safe
  default OPEN = pause qualification = DO NOTHING; transitions are
  insert-only risk_events + notifications.
- Reservation invariants: available+reserved+in_flight consistent with
  session totals; idempotent Reserve by opportunity id; settle/release
  exactly once; violations trip the simulation-inconsistency breaker at
  CRITICAL.
- Drawdown/daily-loss trips require operator acknowledgement to resume
  (no silent auto-resume by default).

Tests: boundary tables per limit, monotonicity property, -race storms on
reservation, chaos per trigger, replay determinism of decisions.
