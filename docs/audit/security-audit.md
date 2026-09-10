# Application security audit

Audited tree: `master` at `abddb55` (2026-09-09). `docs/security.md` was read
first and each stated control was verified against code. Findings are
ranked; the live-trading boundary, Paddle webhook verification, SSRF
hardening, CSRF coverage and the secrets registry all hold. The failures
cluster in revocation, tenancy, and one path that leaks a secret into a
response.

## Findings (ranked)

### S1 · P1 · Login throttling keys on `r.RemoteAddr`, which behind the shipped ingress is one proxy IP — unauthenticated global lockout
- Evidence: `internal/api/auth.go:594` `host := r.RemoteAddr`; no `X-Forwarded-For`/`X-Real-IP` handling exists anywhere (`grep -rn "X-Forwarded-For\|X-Real-IP\|RemoteAddr" --include=*.go` → that one line). `internal/auth/session.go:109` refuses on `IPThrottle` (`auth.NewThrottle(20, time.Minute, 10*time.Minute)`, `internal/app/components.go:1244`); success resets only the per-account key (`session.go:156`). `deploy/helm/arb-platform/templates/ingress.yaml:24` routes `/api` through the cluster Ingress, so every request arrives with the ingress controller's address.
- Impact: 20 failed logins from any source within 60 s lock out every account for 10 minutes, repeatably. `sessions.ip` and `audit_events.ip` record the proxy address, so the forensic IP promised by `docs/security.md:109` is worthless in deployment.
- Action: trusted-proxy configuration (`ARB_TRUSTED_PROXIES` / hop count); derive the client address from the rightmost untrusted `X-Forwarded-For` entry and fail closed to `RemoteAddr` when unconfigured; reset `IPThrottle` on success; consider progressive delay rather than a hard block.
- Validation: 20 failures with distinct e-mails from one IP, then a valid credential for a 21st account still succeeds; with `X-Forwarded-For` set, `audit_events.ip` differs per client.

### S2 · P1 · `smtp_url` can reach the log stream, the database and an authenticated API response
- Evidence: `internal/notification/email.go:97` wraps `url.Parse(raw)` errors; Go's `*url.Error` embeds the raw URL, verified: `url.Parse("smtp://alerts:S3cr3t%zzPass@smtp.example.com:587")` → `parse "smtp://alerts:S3cr3t%zzPass@…": invalid URL escape "%zz"`. `internal/screener/alerts/dispatch.go:166` logs the error and persists `err.Error()` as `DeliveryOutcome.Reason` (`internal/screener/service.go:98`), read back at `internal/storage/screener.go:272` and served by `GET /api/v1/screener/events` (`internal/api/screenerapi.go:622`) to `PermScreenerView`, which `RoleViewer` holds (`internal/auth/rbac.go:49`) and any API key with the `read` scope. The log redaction net is an exact-key match over ten names (`internal/app/logging.go:13`) that excludes `error`, `url`, `smtp_url`, `database_url`.
- Impact: an `smtp_url` whose password contains a `%` not followed by two hex digits (permitted by `ValidateValue`, `internal/secrets/registry.go:127`) is written to logs, stored in `screener_events` and served to the lowest-privileged role — contradicting `docs/security.md:33-36`.
- Action: never wrap a parse error over a secret (return a sentinel); bound `DeliveryOutcome.Reason` to an enum plus correlation id; make `sensitiveKeys` a suffix/substring match (`*_secret`, `*_token`, `*_key`, `*_url`, `*password*`) and add a handler-level scrubber for credential-bearing URIs.
- Validation: store `smtp://u:p%zz@h:587`, fire an e-mail alert, assert `p%zz` appears in neither captured logs nor `GET /api/v1/screener/events`.

### S3 · P1 · API keys survive account disable, role change and membership removal
- Evidence: `internal/apikey/apikey.go:186` `Authenticate` checks only `k.Active()` (`RevokedAt == nil`, line 77); `internal/api/auth.go:377` `resolveAPIKeyPrincipal` looks up the organisation but not `users.status` or `memberships`. The session path does both (`internal/storage/authstore.go:316` joins `users … status <> 'disabled'`; `internal/auth/admin.go:196` revokes sessions on disable). Nothing in `AdminService` or `handleOrgMemberRemove` (`internal/api/orgapi.go:151`) touches `api_keys`. The package has no tests.
- Impact: disabling a compromised or departed account leaves every API key it minted live, with read and (per scope) `rules:write` / `templates:write` / `paper:write` access.
- Action: join `users` in `ByPrefix`/`Authenticate` and refuse disabled owners; require a live membership for `(org_id, user_id)`; cascade-revoke on disable and member removal; add `expires_at` with a default maximum lifetime; add the package's first tests.
- Validation: create a key, disable the owner, assert bearer auth returns 401; same for member removal.

### S4 · P1 · Tenant isolation never engages: every account is force-joined to the platform organisation and `ContextForUser` always resolves to it
- Evidence: `POST /api/v1/users` is the only account-creation path (no sign-up route). `internal/storage/authstore.go:164` inserts a `memberships` row for `tenancy.PlatformOrgID` on every create; `internal/storage/tenancy.go:50` selects the membership `ORDER BY m.created_at ASC, m.org_id ASC LIMIT 1`; `PlatformOrgID = 1` (`internal/tenancy/tenancy.go:15`) is written first and has the lowest id, so it always wins. A later `POST /api/v1/orgs` or `POST /api/v1/org/members` adds a row that can never be selected.
- Impact: `Principal.OrgID` is 1 for every API-created account, so every `orgFilter` predicate (`internal/storage/screener.go:24`) resolves to the platform org: intended tenants share one organisation, `GET /api/v1/org/members` discloses every account's e-mail, `GET /api/v1/org/api-keys` lists every key, the screener corpus and billing subscription are common, and the risk-acknowledgement gate is exempted (`internal/api/auth.go:203`). The multi-tenant surface is inert in practice.
- Action: stop writing the platform membership from `CreateUser`; make organisation assignment explicit at creation (default to the platform org only for `platform_admin`); until then prefer the non-platform membership in `ContextForUser`.
- Validation: create a user, create an org with them as owner, log in, assert `GET /api/v1/auth/me` reports the new org (today: 1).

### S5 · P2 · Console RBAC has no organisation dimension; `PermViewSystem` (held by VIEWER) exposes operator-only surfaces
- Evidence: `internal/auth/rbac.go:46` grants `PermViewSystem` to `RoleViewer`; routes gated on it alone: `GET /api/v1/platform/settings` (`internal/api/platformapi.go:87`, embeds `Telegram.Allowlist`, fees, paper balances), `GET /api/v1/secrets` (`internal/api/secretsapi.go:38`, inventory with `updated_by` and the master-key fingerprint; only the exchange group is filtered), `GET /api/v1/config` (`internal/api/configapi.go:58`), `GET /metrics` (`internal/api/server.go:351` when `ARB_METRICS_ADDR` is unset). `requirePerm` consults only `auth.Can(principal.Role, p)` (`internal/api/auth.go:509`).
- Action: split `view:system` (tenant-safe) from `view:platform` (operator-only, `platform_admin`); move the four routes; strip `Telegram.Allowlist` for non-platform-admins regardless.

### S6 · P2 · Bootstrap admin is re-upserted on every process start, resetting password, role and status
- Evidence: `internal/app/components.go:1207-1232` runs `bootstrapAdmin` unconditionally when `ARB_ADMIN_EMAIL`/`ARB_ADMIN_PASSWORD` are set; `internal/storage/authstore.go:61` `ON CONFLICT (email) DO UPDATE SET password_hash …, role …, status …`. `.env.example:53` and `docs/deployment.md:286` claim the opposite; the Helm chart injects `ARB_ADMIN_PASSWORD` permanently (`templates/externalsecret.yaml:32-33`); `scripts/create-secret.sh:17-20` copies `.env.example` (placeholder password) into `.env`; no minimum length is enforced on the env value.
- Impact: a standing backdoor: every restart re-grants ADMIN, resets the password to the env value and re-enables a disabled account.
- Action: skip bootstrap when `users` is non-empty (or `DO NOTHING`); enforce `MinPasswordLength`; remove the password from steady-state deployment; correct the docs.
- Validation: change the password and disable the account through the console, restart, assert both survive.

### S7 · P2 · `screener_settings` is a single global document with no `org_id`
- Evidence: `migrations/000010_screener.up.sql:13` (no `org_id`); `migrations/000013_tenancy.up.sql:65-70` adds `org_id` to every sibling table but not this one; `internal/storage/screener.go:44` `UPDATE screener_settings SET active = FALSE WHERE active` with no org predicate; reached by `POST /api/v1/screener/settings` (`internal/api/screenerapi.go:67`) on `PermScreenerConfig` + CSRF only.
- Action: add `org_id` with `orgFilter`, or gate the route and `/screener/reports/run` behind `requireOnlyPlatformAdmin` and document the document as platform-global.

### S8 · P2 · Provider-group secrets sit one privilege level below the exchange group; ADMIN does not imply `platform_admin` after promotion
- Evidence: `internal/api/secretsapi.go:39` gates `PUT /api/v1/secrets/{name}` on `PermSystemConfig`; `exchangeGroupGate` (line 93) requires `platform_admin` only for the exchange group; provider entries include `paddle_webhook_secret`, `paddle_api_key`, `telegram_bot_token`, the advisor API key and `smtp_url` (`internal/secrets/registry.go:53-68`). `platform_admin` is set only at insert (`internal/storage/authstore.go:66,157`); `UpdateUserRole` (line 238) does not set it.
- Impact: a promoted console ADMIN can overwrite the Paddle webhook secret (then forge `subscription.updated` events to grant any package), retarget the Telegram bot, or point alert e-mail at a hostile relay.
- Action: gate secret writes on `requireOnlyPlatformAdmin` for the whole registry; make `UpdateUserRole` consistent with `CreateUser`.

### S9 · P2 · Audit-table immutability is documented but enforced by nothing in the repository
- Evidence: `docs/security.md:108` and `migrations/000001_initial.up.sql:3` state the policy; `grep -rn "REVOKE\|GRANT" migrations/ deploy/` → nothing; `docker-compose.yml:6` runs migrations and the app as the same `arb` role; Terraform/Helm fill the DSN out of band with no grant automation.
- Action: a migration creating an `arb_app` role with `INSERT, SELECT` only on `audit_events` (or a `BEFORE UPDATE OR DELETE` trigger that raises); assert the grant in the restore drill.
- Validation: `DELETE FROM audit_events` from the app DSN must fail.

### S10 · P2 · `POST /api/v1/org/members` adds arbitrary user ids without consent and discloses e-mail
- Evidence: `internal/api/orgapi.go:117` passes `body.UserID` straight to `AddMember`; `internal/storage/tenancy.go:145` returns `u.email` for every member; `GET /api/v1/org/members` and `GET /api/v1/org/api-keys` are `requireAuth` only (`orgapi.go:38`).
- Action: invitation flow keyed by e-mail; refuse users who hold a membership elsewhere; gate roster and key listing on `requireOrgManager` or strip e-mail/prefix for non-managers.

### S11 · P2 · WebSocket has authentication but no per-topic authorization
- Evidence: `internal/api/server.go:330` `requireAuth(s.handleWS)`; `internal/api/ws.go:50` passes no principal; `internal/realtime/hub.go:103` `Subscribe` accepts any registered topic (`alerts`, `health`, `campaigns`, `replays`, `scanner`, `recordings`, `cycles`) whose HTTP equivalents require `PermViewDashboard`/`PermViewSystem`/`PermViewPortfolio`.
- Action: thread the principal into `HandleClientOp`, map topics to permissions via `auth.Can`, reject unauthorised subscribes with an error frame.

### S12 · P3 · `VerifyPassword` accepts unbounded Argon2 parameters from the stored hash
- Evidence: `internal/auth/password.go:71-85` parses `m,t,p` from the PHC string and derives with them under a four-slot semaphore (line 35). Not remotely reachable today (hashes are produced only by `HashPassword`), but any write primitive on `users.password_hash` becomes an OOM.
- Action: clamp on parse (`mem ≤ 256 MiB`, `iters ≤ 10`, `par ≤ 8`) and treat below-current memory as a rehash signal.

### S13 · P3 · HTTP server sets only `ReadHeaderTimeout`
- Evidence: `internal/api/server.go:245` (`ReadHeaderTimeout: 5s`; no `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `MaxHeaderBytes`). Request bodies are bounded per handler.
- Action: `ReadTimeout` 30 s, `WriteTimeout` 60 s (exempt the WebSocket route), `IdleTimeout` 120 s, `MaxHeaderBytes` 64 KiB.

### S14 · P3 · CI supply-chain gates partly missing; no action or base image digest-pinned; workflow input interpolated into a shell step
- Evidence: `.github/workflows/ci.yml` has gosec (via golangci-lint), govulncheck (`go install …@latest`, line 71) and gitleaks but no `npm audit` and no container scan; actions pinned by major tag (`actions/checkout@v4`, `setup-go@v5`, `golangci-lint-action@v8`, `gitleaks-action@v2`); `Dockerfile:4,14` `golang:1.25`, `alpine:3.20`; `deploy/docker/web.Dockerfile:4` `node:22-alpine`; `.github/workflows/deploy.yml:56` interpolates `${{ inputs.tag }}` into a `run:` block.
- Action: SHA-pin actions, pin govulncheck, digest-pin images, add `npm audit --audit-level=high` and Trivy for both images, pass `inputs.tag` through `env:`.

### S15 · P3 · Marketing site ships no security headers and renders markdown with raw HTML passthrough
- Evidence: `site/next.config.ts` (`output: "export"`, no `headers()`); `site/src/lib/markdown.ts:3` passes raw HTML through to 12 `dangerouslySetInnerHTML` sites (e.g. `site/src/app/legal/[slug]/page.tsx:32`). The console itself is correctly hardened (`web/next.config.ts`: CSP, `frame-ancestors 'none'`, nosniff, `no-referrer`, `Permissions-Policy`).
- Action: emit the header set from the static host config kept under `deploy/`; add a sanitiser with an allow-list to `renderMarkdown`.

## Verified correct

- Execution boundary: `LiveExecutor.ExecuteCycle` always returns `ErrLiveTradingDisabled` (`internal/execution/executor.go:139`); `platform.mode LIVE` rejected by name (`internal/platform/modes.go:57`); `entitlements` refuses `execution.live = true` in `validate.go:69` and again in `resolve.go:63`.
- No exchange trading key is readable by any code path: `secrets.Manager.Get` refuses the exchange group before storage (`internal/secrets/vault.go:201`, `IsConsumable` in `registry.go:94`); the registry is closed; `internal/exchange/` contains no credential, signing or HMAC code; the only two `Manager.Get` call sites are the advisor key and `smtp_url` (`internal/app/components.go:214,776`).
- Vault crypto: AES-256-GCM, fresh random 12-byte nonce per seal, secret name as AAD, per-row key fingerprint checked before any crypto (`internal/secrets/crypto.go:107,118`); rotation surfaces `readable:false` with a reason (`vault.go:294`). Values never leave the vault: `SecretsAdmin` has no read method; PUT bodies are decoded into `[]byte` and zeroed (`secretsapi.go:127,144`).
- Paddle webhook: HMAC-SHA256 over `ts:body` with constant-time compare, key rotation, ±5 min replay window (`internal/billing/paddle/signature.go:21-63`); verified against the raw body before parsing (`service.go:75`); idempotent with retry on crash-before-processed (`internal/storage/billing.go:23-38`).
- CSRF: all 51 mutating routes carry `requireCSRF` except login (pre-session) and the HMAC-authenticated webhook; logout included; token is a stateless HMAC over the session token held in module state, never `localStorage` (`web/src/lib/api/client.ts:70-88`).
- Sessions: 256-bit tokens stored as SHA-256 digests; `HttpOnly + Secure + SameSite=Lax`; 12 h absolute expiry; disabled users filtered at lookup; role change, disable, admin reset and self-service change all revoke sessions (`internal/auth/admin.go:169,196,223,263`).
- Passwords: Argon2id `t=3, m=64 MiB, p=2`, random salt, PHC encoding, constant-time compare, unknown-user timing equalised, process-wide concurrency bound of 4, per-user throttle on self-service change, 12-character minimum.
- SSRF: webhook sink resolves and dials the vetted IP atomically, refuses loopback/RFC1918/link-local/metadata/CGNAT/multicast/unspecified and redirects (`internal/notification/webhook.go:64-143`); scheme validated at rule save; `WebhookSecret` zeroed on every response path.
- SQL: every query parameterised; the two dynamic fragments are a `$n` placeholder and a map-allow-listed group expression (`internal/storage/analytics.go:61`); no `os/exec` anywhere.
- Path traversal: recording ids constrained to `^[A-Za-z0-9_-]{1,64}$` before `filepath.Join`; report file names through `safeName`; report reads are database-backed.
- Prompt injection: `ai.Input` is typed aggregates only (`internal/ai/ai.go:34-52`); output strict-decoded with unknown-field and trailing-content checks, bounded lengths and counts, confidence in [0,1], and every recommended parameter must survive `strategy.ApplyChange`; approval re-applies the approver's own per-section RBAC on web and Telegram.
- Telegram: allow-list before dispatch with generic denial; permission checked before execution; strict command set; single-use, user-bound, 15-minute HMAC callback nonces verified before consumption; bot token stripped from transport errors.
- No committed credential (targeted scans for vendor key shapes, PEM, GitHub/Slack tokens match only test fixtures); `.env` absent; `.gitignore` excludes `.env*`; containers run as non-root (`Dockerfile:19`, `deploy/docker/web.Dockerfile:21`).
- Console headers: full CSP with `frame-ancestors 'none'`, `base-uri 'self'`, `form-action 'self'`, nosniff, `X-Frame-Options: DENY`, `no-referrer`, `Permissions-Policy`; `writeJSON` sets nosniff on every API response; no `dangerouslySetInnerHTML` in the console; no `NEXT_PUBLIC_*` variables; correlation ids pattern-bounded before echo (`internal/api/auth.go:584`).
- `go build`, `go vet` clean; `go test ./internal/auth/... ./internal/secrets/...` passes. `govulncheck` could not run in this environment (vulnerability database fetch blocked); CI runs it on every push.
