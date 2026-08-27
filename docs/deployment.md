# Deployment: recording real feeds and running the §80 campaign

This runbook takes the platform from a fresh host to a completed
profitability-validation campaign (T-046). Everything runs in Docker
Compose; the only external dependency is outbound HTTPS/WSS to Binance
(`api.binance.com`, `stream.binance.com:9443`). No API keys are needed —
recording uses public market-data endpoints only, and the platform never
places orders by design (`ErrLiveTradingDisabled`).

## 1. Prerequisites

- A host with Docker + Compose v2, outbound internet, and disk for
  recordings (see sizing below).
- This repository checked out.
- Optional `.env` next to `docker-compose.yml`:

```dotenv
POSTGRES_PASSWORD=change-me
ARB_SYMBOLS=BTCUSDT,ETHUSDT,ETHBTC,BTCUSDC,ETHUSDC,USDCUSDT
ARB_STARTING_ASSETS=USDT,USDC
# only for the paper profile:
ARB_ADMIN_EMAIL=you@example.com
ARB_ADMIN_PASSWORD=a-strong-password
```

## 2. Start recording

```sh
make record          # = docker compose --profile record up -d --build
```

This builds the image, starts PostgreSQL, applies all migrations, and
launches `arbd` in RECORD mode. The recorder captures every raw WS depth
frame and REST snapshot for the configured symbols into rotated,
zstd-compressed segment files under the `recordings` volume, and
registers each closed segment in `market_recording_metadata`.

Find the session id (you need it for the campaign):

```sh
docker compose logs arbd-record | grep "recording enabled"
# ... recording enabled dir=/recordings session=<SESSION-ID>
```

### Verify it is actually capturing

```sh
# frames flowing and no resync storms:
curl -s localhost:8080/healthz
docker compose exec arbd-record ls -lh /recordings/<SESSION-ID>/

# segment rows registering (repeat later; the list should grow):
docker compose exec db psql -U arb -d arb \
  -c "SELECT id, started_at, ended_at, jsonb_array_length(segment_files) AS segments
      FROM market_recording_metadata ORDER BY started_at DESC LIMIT 5;"
```

### Sizing and duration

Six liquid symbols at 100 ms depth updates produce very roughly
0.5–2 GB/day compressed, dominated by BTC/ETH volatility. For a first
campaign record **at least a few hours spanning active market hours**;
for numbers worth arguing about, record **multiple sessions across
different days and regimes** (calm, volatile, weekend). Segments rotate
every 200k frames; a crash loses at most the open segment.

Stop when done:

```sh
make record-stop
```

## 3. Run the §80 campaign

```sh
make campaign RECORDING=<SESSION-ID>
```

This replays the recording through the complete pipeline — recorded
books → the real scanner → the deterministic risk engine → the simulated
executor → portfolio — once per (scenario × seed) over the §80 stress
grid:

| axis | scenarios |
|---|---|
| higher fees | +5 bps, +10 bps per leg |
| higher latency | ×2, ×4 |
| worse fills | executor sees 50% of displayed depth |
| lower liquidity | the whole world at 50% depth |
| combined | fees +10, latency ×3, both depth cuts |

Outputs land in the recordings volume:
`campaign-<SESSION-ID>.md` (the report) and `.json` (raw evidence).
Copy them out with:

```sh
docker compose cp arbd-record:/recordings/campaign-<SESSION-ID>.md .
```

The report's **Verdict** section carries the honest §80 judgement,
including the mandatory flag when the strategy is profitable only under
perfect conditions — a system like that is worthless and the report says
so. One recording is one sample: repeat over several recordings before
trusting any positive result.

### Fidelity notes (what the simulation does and does not claim)

- Fills are simulated with seeded latency against books that **continue
  to move during simulated waits** (frames recorded inside the wait
  window are applied before the fill is priced), so adverse drift is
  modeled from real data.
- Limit-IOC semantics, partial fills, fee-in-kind, quantization, and
  intermediate exposure follow the audited simulation engine — the same
  code paper trading runs.
- Self-impact is NOT modeled: simulated orders do not consume the
  recorded book for other participants, and the recording obviously
  contains no reaction to orders that were never sent. Treat results as
  an upper bound and lean on the stressed scenarios, not the baseline.

## 3b. Console-driven recording and campaigns

Everything in §2–§3 can also be driven from the operations console —
no terminal needed once the stack is up:

```sh
docker compose --profile paper up -d --build   # engine + API on :8080
cd web && npm run build && npm start           # console
```

Sign in and open **Campaigns**:

- **Recorder** card — start/stop an in-process recording session
  (permission `recordings:control`, OPERATOR and ADMIN). The session id,
  uptime, frames written/dropped and closed segments update live; stop
  waits for the last segment to close and register, so the session is
  immediately usable. `ARB_MODE=RECORD` still auto-starts a session at
  boot for headless deployments.
- **Recorded sessions** — the `market_recording_metadata` catalog with
  a *Run campaign* button per closed session.
- **Run §80 campaign** — the same grid × seeds the CLI runs
  (`internal/campaign` is shared by both), executed in the background
  inside `arbd` (permission `campaigns:run`); progress streams over the
  console WebSocket. Reports are written next to the recording
  (`/recordings/<SESSION-ID>/campaign-<RUN-ID>.md|.json`) and kept in
  `campaign_runs`, and the **Verdict** flags are shown verbatim.

Every start/stop/run is audited (`audit_events`, source=web). Recording
still needs no API keys and the engine still cannot place orders.

## 3c. Settings & restart from the console

Symbols, starting assets, per-asset paper balances, venue enablement and
fees, and the Telegram allowlist live in a second versioned document
(`internal/platform`, table `platform_settings`) — separate from the
strategy config in §80's campaign grid, and separate from
`ARB_SYMBOLS`/`ARB_STARTING_ASSETS`/`ARB_PAPER_BALANCE`/
`ARB_TELEGRAM_ALLOWLIST`, which only seed **version 1** on a fresh
install and are ignored afterward. Open **Settings** in the console:

- **Markets & assets** and **Venues & fees** edit the document (ADMIN
  only: `exchange:config` for venues/symbols/fees, `system:config` for
  paper balances and the allowlist); every field there is tagged
  **On restart**. A **Preview** call (`POST
  /api/v1/platform/settings/preview`, `view:system`) dry-runs the
  topology (`graph.Build`) against the live `exchangeInfo` before you
  can apply, so a bad symbol list is rejected on save, never discovered
  when the engine restarts. `token_discount` is rejected outright at
  validation (no pay-asset debit ledger exists yet, so the discounted
  fee rate would make paper P&L optimistic with nothing to account for
  it); `GET /api/v1/platform/venues` (`view:system`) serves the
  compiled venue/discount table (pay asset, rate, whether it applies to
  API trades, and an honest `modeled: false`) so the console renders —
  and correctly disables — that toggle from data instead of hardcoding
  it.
- **Notifications** carries the Telegram allowlist, tagged **Immediate**
  once the bot is running — revoking a user takes effect without a
  restart. If the process booted with an empty allowlist the bot was
  never constructed; adding the first entry still needs a restart.
- **Restart engine…** (`POST /api/v1/engine/restart`, `system:config`,
  type `RESTART` to confirm) applies every pending restart-scoped
  change: it stops an active recording first only if you check that
  box, pauses paper trading while legs settle, waits for the current run
  to drain (persisted opportunities/cycles/reports are never touched),
  then reconnects the feed and rebuilds the triangles under one stable
  engine process — never a process restart. It refuses (409) while a
  campaign run is in progress or a recording is active without
  `stop_recording`, and while a restart is already under way.

## 3d. Operating mode, AI advisor, log level, origin and secrets (T-059..T-061)

The same versioned document (`platform_settings`) now also carries the
fields that used to be env-only. Every one of them is validated,
versioned, audited, and tagged by the backend's `field_timing` as either
**Immediate** (hot) or **On restart**; the environment seeds **version 1
only** and is ignored afterward (the boot log says so).

| Setting | Path | Timing | Notes |
|---|---|---|---|
| Operating mode | `platform.mode` | On restart | `MARKET_DATA`, `RECORD`, `PAPER`. `SHADOW` is listed but `available:false` (not wired into the engine). `LIVE` is refused by name — this platform never places real orders; `REPLAY`/`BACKTEST` are batch runs started from Replays/Campaigns, not process modes. An `ARB_MODE` of `SHADOW`/`REPLAY`/`BACKTEST` seeds `MARKET_DATA` and logs the substitution; so does a pre-expansion document whose stored venues cannot run under the env mode (e.g. `paper_enabled=false` with `ARB_MODE=PAPER`) — the boot log names it, and the next operator write persists the complete document. |
| Log level | `platform.log_level` | Immediate | `debug|info|warn|error`. `debug` logs every rejected opportunity — a measurable cost on the consumer path. |
| Console origin | `platform.allowed_origin` | Immediate | Strict `scheme://host[:port]`. Same-host origins are always admitted, so a wrong value cannot lock the console out. |
| AI advisor | `ai.*` | Immediate | `enabled`, `provider` (`anthropic` or `fake`; `openai` is listed as not built), `model`, `schedule.{hourly_minutes,daily_hours,weekly_hours}` (0 disables a cadence), `budget.{max_analyses_per_day,max_output_tokens}`. The daily cap is a per-process UTC counter and resets on a process restart. Enabling `anthropic` without a resolvable key is accepted and answered with a `warnings` entry; `GET /api/v1/ai/status` then reports `enabled:true, running:false, reason:"no anthropic_api_key"`. |
| Telegram mute | `telegram.disabled` | Immediate | Mutes commands and pushes together. The field is deliberately negative so documents written before it existed keep delivering. |

Routes (all under `/api/v1`, `view:system` to read, `system:config` +
CSRF to write):

- `GET /platform/capabilities` — `{modes, venues, ai_providers,
  log_levels, secrets:{vault_configured,key_id|reason}, field_timing}`.
  Every list entry is `{id, available, reason?}`; the console renders
  from this, never from hardcoded enums. `GET /platform/venues` is an
  alias over the same venue table (binance available; okx/bybit/bitget/
  gate/mexc listed with the task that blocks them). Enabling an unbuilt
  venue is `400 connector_unavailable` naming that task.
- `GET /ai/status` (`view:dashboard`) — `{enabled, running, reason,
  provider, model, key_source, analyses_today, max_per_day,
  last_analysis}`.
- `GET /system/status` reports the **running** mode; the `health` hub
  topic carries `mode: {running, configured}` so a pending mode change
  can be annotated until the restart applies it.

**Changing the mode.** Apply the new `platform.mode`, then **Restart
engine…**. Leaving `RECORD` with an active recording is refused
(`409 recording_active`) unless you tick *Stop the active recording*;
the Supervisor then stops it before cancelling so the last segment
closes cleanly. Entering `PAPER` requires `paper_enabled` on every
enabled venue and a `paper.balances` entry per starting asset — both
are validated at apply time, never discovered at restart — and the new
run mints a fresh paper session. Leaving `PAPER` closes the outgoing
session; persisted cycles and opportunities are kept, in-memory balances
reset.

**Secrets vault.** Provider credentials (`anthropic_api_key`,
`telegram_bot_token` — a closed registry; no exchange key can be stored)
live in the `secrets` table (migration 000008), encrypted with
AES-256-GCM under `ARB_SECRET_KEY`. Generate a key once and keep it out
of the repo:

```sh
ARB_SECRET_KEY=$(head -c 32 /dev/urandom | base64)   # must decode to exactly 32 bytes
```

- Unset or invalid key → the vault stays closed (logged at boot), the
  platform keeps running on the env-provided values, `GET /secrets`
  answers `vault_configured:false` with the reason, and writes answer
  `503 vault_unavailable`. The vault also needs `ARB_DATABASE_URL`.
- `GET /api/v1/secrets` (`view:system`) lists presence, source
  (`vault`/`env`), readability, `updated_at`/`updated_by` and `applies`
  — never a value, never a prefix.
- `PUT /api/v1/secrets/{name}` `{"value":"…"}` (`system:config`, CSRF)
  stores the value (trimmed; printable ASCII; ≥20 and ≤4096 chars) and
  audits `secret.write`. The Anthropic key applies **immediately**
  (the advisor is rebuilt on the next settings swap and on the write);
  the Telegram token applies on the next **process** restart — the
  response's `applies` field says which.
- `DELETE /api/v1/secrets/{name}` removes the row so resolution falls
  back to the environment; audited as `secret.delete`.
- A row written under a different `ARB_SECRET_KEY` is reported
  `present:true, readable:false, reason:"encrypted under key …; this
  process holds …"` and resolution falls through to env. Rotating the
  master key is manual: `DELETE` every row, restart with the new key,
  `PUT` every value.

## 4. Optional: full paper deployment

```sh
docker compose --profile paper up -d --build
```

Runs the complete engine (scanner, risk, paper trading, API on :8080)
against live feeds, with the console servable separately via
`cd web && npm run build && npm start`. Set the admin credentials in
`.env` first; the API refuses logins without configured users.

## 5. Operational notes

- `/metrics` (Prometheus) is RBAC-gated on the same port; set
  `ARB_METRICS_ADDR` for a private unauthenticated exporter port
  instead when scraping from inside the network.
- The recorder degrades honestly: overflow drops are counted
  (`recorder_frames_dropped`) rather than stalling the feed; sequence
  gaps trigger book resyncs which the recording itself captures.
- Postgres is the source of truth for market metadata (instrument
  rules), the recording catalog, and the stream table the campaign
  needs — keep the `pgdata` volume alongside the `recordings` volume.
