# Security Model

Status: DESIGNED (Phase 1). Enforced by `internal/auth`, `internal/audit`,
review gates (security-engineer agent), and CI scanners.

## 1. Threat model (what we defend against)

Assets: exchange API credentials (demo/read-only only, but still secrets),
user credentials/sessions, strategy configuration, audit integrity, and
the execution boundary itself. Adversaries considered: remote attackers
against the console/API, malicious or compromised Telegram senders,
prompt-injection via any untrusted text reaching the AI advisor,
supply-chain vulnerabilities, and operator error. Out of scope: physical
access, targeted exchange-side compromise.

## 2. Execution boundary (highest-value invariant)

- `LiveExecutor` compiles in but every call returns
  `ErrLiveTradingDisabled`. No config flag, env var, build tag, test hook,
  or API endpoint enables it. Removing this restriction is a human code
  change that review must treat as a project-scope change.
- Exchange credentials, when used at all, are demo/testnet or read-only;
  never with withdrawal/transfer permission; IP-allowlisted where the
  venue supports it. Public market data requires no credentials.
- AI, Telegram, and web inputs cannot reach any execution pathway except
  through the same application services, which end at simulated executors.

## 3. Secrets

- No secrets in git — enforced by .gitignore discipline + secret scanning
  (gitleaks) in CI; `.env` files are local-only, `.env.example` documents
  shape with placeholders.
- No secrets in logs (slog redaction of known-sensitive keys), in AI
  prompts (prompt builders consume typed summaries, not raw config), in
  API responses (config endpoints return masked references), or in
  Telegram output.
- Deployed environments read secrets from a secret manager / injected env
  at runtime; the app never persists them to the database. Exchange keys
  entered via the console are stored encrypted (AES-GCM with a key from
  the environment), displayed only as last-4 after entry.

## 4. Authentication & sessions

- Passwords: Argon2id (tuned params documented in code); no plaintext
  anywhere; constant-time comparison; password change revokes other
  sessions.
- Sessions: random 256-bit tokens, stored server-side ONLY as SHA-256
  digests (a leaked sessions table yields no usable bearer tokens);
  revocable; disabling a user invalidates their live sessions at lookup;
  HttpOnly + Secure + SameSite=Lax cookies; absolute expiry (12h). Idle
  expiry is not implemented — the absolute lifetime is the bound.
- CSRF: double-submit token header required on all mutating browser
  endpoints (cookie-authenticated), logout included; WS upgrade validated
  against session + Origin allowlist.
- Rate limiting: per-(account,IP) and per-IP login throttling with
  lockout; Argon2 verification concurrency is bounded process-wide so
  login floods cannot exhaust memory.
- MFA: architecture reserves an enrollment/verification step in the login
  flow (TOTP first); not required for MVP but the session model records
  `mfa_enrolled`.

## 5. Authorization (RBAC)

Roles ADMIN / OPERATOR / VIEWER enforced in the application service layer
(single source of truth for web AND Telegram). Route/service matrix lives
with the auth package and is covered by denial tests for every mutating
endpoint. Frontend hiding is UX, never authorization. Telegram user IDs
map to platform users; unmapped IDs get no response beyond a generic
denial.

## 6. Untrusted input & prompt-injection defenses (SKILL.md §68)

Untrusted: all Telegram text, web form input, exchange messages and
metadata (symbol names, statuses), news, external docs, and AI OUTPUT.

- Parsers are strict and typed; no free-text ever becomes config, SQL,
  shell, or prompt scaffolding.
- AI prompt builders accept only typed, summarized inputs (metrics,
  distributions, reason-code counts) — never raw books, never config
  containing secrets, never verbatim user/Telegram text.
- AI output is schema-validated (JSON schema, strict) before use;
  validation failure = rejected analysis, logged, alerted at WARNING.
- No external content can change permissions, risk policy, execution
  boundary, or secret policy — these live in code and validated config
  with RBAC + audit, with no AI/Telegram mutation path at all (AI
  recommendations only become config through explicit human approval).

## 7. Transport & headers

TLS terminates at the reverse proxy in deployment (dev: localhost).
Security headers on the console: CSP (self + ws endpoint; no inline
scripts beyond Next.js hashed runtime), X-Content-Type-Options,
Referrer-Policy, frame-ancestors 'none'. WS same-origin policy enforced.

## 8. Supply chain & CI security

- Go: `govulncheck` + `gosec` in CI; dependencies pinned via go.sum;
  minimal dependency policy (decimal, pgx, ws, otel, chi/std router).
- Frontend: `npm audit` gate + lockfile; no runtime CDN dependencies.
- Secret scanning (gitleaks) on every push; container scanning when images
  are introduced.
- CI fails closed on P0 findings.

## 9. Audit

Every auth event, config change, risk-limit change, AI decision, paper
engine control action, and Telegram action produces an insert-only audit
event (actor, source, before/after, IP, correlation_id). The application
role lacks UPDATE/DELETE on audit tables; the console can filter but not
delete. Audit review is part of the daily report.

## 10. Security testing

- Unit: authz denial matrix, CSRF enforcement, session expiry/revocation,
  rate-limit behavior, secret redaction in logs.
- Integration: login throttling, Telegram allow-list bypass attempts,
  schema-validation rejection of malformed AI output, masked secret
  round-trips.
- E2E: RBAC restrictions per role, session fixation/logout, security
  headers present.
- Chaos/adversarial: prompt-injection corpus through Telegram/web inputs
  asserting zero config/execution effect.
