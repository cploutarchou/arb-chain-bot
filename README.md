# arb-chain-bot

A professional single-exchange **triangular-arbitrage research and
paper-trading platform**: Go backend (real-time order books, deterministic
risk engine, realistic paper execution), Next.js operations console,
Telegram control surface, PostgreSQL persistence, record/replay, and an AI
advisor with human-approved recommendations.

**No live trading.** The execution boundary ships disabled by design:
`LiveExecutor` always returns `ErrLiveTradingDisabled`. There is no
guaranteed profit; the platform's job is to measure — honestly — whether
any net edge exists after fees, slippage, latency, and failure risk.

## Scope

Triangular arbitrage only: three-leg cycles (e.g. USDT → BTC → ETH → USDT)
where the complete cycle exists on ONE exchange. No cross-exchange
arbitrage, no directional strategies, no market making.

First exchange: **Binance** (see `docs/research/final-platform-selection.md`
for the scored comparison of 7 venues). Second: OKX, gated on the
first-exchange definition of done.

## Repository map

| Path | Contents |
|---|---|
| `cmd/` | thin entry points: `arbd` (everything), `api`, `scanner`, `recorder`, `replay`, `worker` |
| `internal/` | platform packages (see `docs/architecture.md` §3) |
| `web/` | Next.js operations console |
| `migrations/` | forward-only SQL migrations (golang-migrate naming) |
| `deploy/` | compose, dashboards, alert rules |
| `docs/` | architecture, research, MASTER_PLAN, runbooks |
| `.claude/` | development skill, agent team, skill resources |

## Development

Requirements: Go 1.24+, Node 22+, Docker (for Postgres), or a local
PostgreSQL 16.

```bash
cp .env.example .env          # never commit real secrets
make up                       # start PostgreSQL (docker compose)
make migrate                  # apply migrations
make all                      # gofmt + vet + tests + build
go run ./cmd/arbd             # run the platform (MARKET_DATA mode)
make web-install && make web-dev   # operations console on :3000
```

Key docs: `docs/architecture.md`, `docs/MASTER_PLAN.md` (task tracker),
`docs/research/` (exchange/framework/fee research), `docs/risk.md`,
`docs/security.md`.

## Status

Under active initial development. `docs/MASTER_PLAN.md` is the source of
truth for what is DONE vs TODO — nothing here claims to work unless its
acceptance criteria and tests pass.
