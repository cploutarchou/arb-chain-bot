# Resource: AI Advisor

Authoritative documents: `docs/security.md` §6, `docs/risk.md` §6.
Implementation home: `internal/ai`.

Boundaries (hard):
- AI is an analyst, off the hot path; provider outage must not affect
  scanning or paper trading.
- Inputs are typed summaries only (metrics, distributions, reason-code
  counts, performance aggregates). Never raw order books, never secrets,
  never verbatim untrusted text (Telegram/web/news/exchange metadata).
- Output is schema-validated JSON (strict); invalid ⇒ rejected + logged +
  WARNING alert. AI text is untrusted input to the rest of the system.
- Recommendations (docs/risk.md §6) carry id, scope, parameter,
  current/recommended value, evidence, confidence, expected effect,
  risks, expires_at. They change nothing until a human approves via web
  or Telegram (RBAC OPERATOR+ where permitted); approval/rejection is an
  audit event; rejection history is never hidden or pruned.

Provider abstraction: `Advisor` interface; Anthropic first implementation
(model configurable; default a current Claude model), OpenAI as an
alternative implementation; a deterministic FakeAdvisor for tests. API
keys via environment/secret manager only.

Scheduled analyses (cmd/worker): hourly health, daily performance, weekly
parameter review. Each produces a persisted AnalysisResult + optional
recommendations + a concise notification. Prompt builders live in code
with versioned templates; prompts are logged WITHOUT payload secrets and
reproducible from stored inputs.
