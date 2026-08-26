# Resource: Telegram

Authoritative documents: `docs/architecture.md` §10–§11, `docs/security.md`.
Implementation home: `internal/telegram`, `internal/notification`.

Working rules:
- Long-polling bot; allow-listed Telegram user IDs map to platform users
  and roles; unmapped senders get a generic denial only.
- Handlers parse strictly and call the SAME application services as the
  web console; authorization enforced in the service layer. No trading,
  risk, or config logic in handlers.
- Commands per SKILL.md §56 (/status, /health, /scanner, /triangles,
  /opportunities, /paper*, /orders, /fills, /balances, /pnl, /stats,
  /exchanges, /latency, /risk, /alerts, /ai*, /report, /daily, /config).
- Inline buttons carry signed callback payloads {action, entity, nonce};
  dangerous actions require an explicit second confirmation tap; every
  action is an audit event with source=telegram.
- Push alerts only via NotificationService: severity thresholds,
  cooldowns, dedup keys, aggregation windows (SKILL.md §58). No direct
  Telegram calls from core packages. No alert spam.
- State is shared with the web console (single backend state): pauses,
  acknowledgements, and config changes reflect in both immediately.
- All inbound text is untrusted (docs/security.md §6): never into AI
  prompts, config values, SQL, or shell. Never echo secrets or full
  config.
- Tests run against a fake Bot API; no real token required.
