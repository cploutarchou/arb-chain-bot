# Resource: Security

Authoritative document: `docs/security.md` (threat model, controls, tests).
Enforcement: security-engineer agent reviews + CI scanners.

Checklist for every change (abbreviated):
- Execution boundary intact: `LiveExecutor` returns
  `ErrLiveTradingDisabled`; no flag/env/test hook enables live orders.
- Secrets: none in git/logs/AI prompts/API responses/Telegram; env for
  local dev; secret manager in deployment; exchange keys demo/read-only,
  no withdrawal/transfer, IP-allowlisted where supported; masked after
  entry.
- AuthN: Argon2id, server-side revocable sessions, HttpOnly/Secure/
  SameSite cookies, CSRF double-submit on mutations, login throttling.
- AuthZ: RBAC (ADMIN/OPERATOR/VIEWER) in the service layer for web AND
  Telegram; denial tests for every mutating endpoint; UI hiding is not
  authorization.
- Untrusted input: Telegram text, web forms, exchange metadata, news, AI
  output. Strict typed parsing; schema-validated AI output; no external
  content can alter permissions, risk policy, execution boundary, or
  secret policy.
- Supply chain: govulncheck + gosec + gitleaks + npm audit in CI; pinned
  deps; fail closed on P0.
- Audit: insert-only events for auth/config/risk/AI/paper/Telegram
  actions with actor, source, before/after, correlation_id.

Findings format: severity P0–P3, evidence file:line, impact, fix,
acceptance criteria → docs/MASTER_PLAN.md.
